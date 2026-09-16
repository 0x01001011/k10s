package k8s

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/lens"
	apiextfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

// A CNPG-shaped pack, declared here rather than reusing the shipped one so a
// pack edit cannot break the mechanism's tests.
const cnpgLikePack = `
name: cnpglike
requires: [cl.example.com/v1]
severities:
  phase:
    ok: ["Cluster in healthy state"]
    default: warn
kinds:
  - key: cl-clusters
    name: Clusters
    short: clc
    group: CLike
    gvr: cl.example.com/v1/clusters
    namespaced: true
    columns:
      - {header: NAME, path: .metadata.name}
      - {header: PHASE, path: .status.phase, severity: phase}
    actions: [describe, cl-promote, cl-unfence]
actions:
  - id: cl-promote
    label: Promote instance
    verb: status-patch
    confirm: typed
    retryOnConflict: true
    params:
      - name: instance
        required: true
        optionsFrom: .status.instanceNames
    patch:
      status:
        targetPrimary: "{{.Params.instance}}"
        phase: Switchover in progress
  - id: cl-unfence
    label: Unfence
    verb: annotate
    annotations:
      cl.example.com/fenced: ""
`

const ackPack = `
name: ackpack
requires: [ack.example.com/v1]
kinds:
  - key: ack-things
    name: Things
    short: ackt
    group: Ack
    gvr: ack.example.com/v1/things
    namespaced: true
    columns: [{header: NAME, path: .metadata.name}]
    actions: [ack-poke, ack-plain]
actions:
  - id: ack-poke
    label: Poke
    verb: annotate
    ack: .status.lastHandledRefresh
    annotations:
      ack.example.com/refresh: "{{.Now}}"
  - id: ack-plain
    label: Plain
    verb: annotate
    annotations:
      ack.example.com/plain: "1"
`

var (
	cnpgGVR = schema.GroupVersionResource{Group: "cl.example.com", Version: "v1", Resource: "clusters"}
	ackGVR  = schema.GroupVersionResource{Group: "ack.example.com", Version: "v1", Resource: "things"}
)

func cnpgCluster(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cl.example.com/v1",
		"kind":       "Cluster",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"status":     map[string]any{"phase": "Cluster in healthy state"},
	}}
}

func ackThing(ns, name, handled string) *unstructured.Unstructured {
	st := map[string]any{}
	if handled != "" {
		st["lastHandledRefresh"] = handled
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "ack.example.com/v1",
		"kind":       "Thing",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"status":     st,
	}}
}

// lensStoreWithPack builds a Store serving ONE synthetic pack, written into a
// temporary lens directory so the shipped builtins are not involved.
func lensStoreWithPack(t *testing.T, packYAML string, gvr schema.GroupVersionResource, listKind string, objs ...runtime.Object) *Store {
	return lensStoreWithKinds(t, packYAML, map[schema.GroupVersionResource]string{gvr: listKind}, objs...)
}

// lensStoreWithKinds is lensStoreWithPack for a pack whose actions reach a
// SECOND resource — Longhorn's detach patches the VolumeAttachment sibling,
// so the fake has to know that GVR's list kind too.
func lensStoreWithKinds(t *testing.T, packYAML string, listKinds map[schema.GroupVersionResource]string, objs ...runtime.Object) *Store {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "pack.yaml", packYAML)
	t.Setenv("K10S_LENS_DIR", dir)

	p := mustPack(t, packYAML)
	res := make([]*metav1.APIResourceList, 0, len(p.Requires))
	for _, gv := range p.Requires {
		res = append(res, &metav1.APIResourceList{GroupVersion: gv})
	}

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		listKinds,
		objs...,
	)
	c := &Client{
		RestConfig:     &rest.Config{Host: "https://fake"},
		Clientset:      fake.NewSimpleClientset(),
		Dynamic:        dyn,
		Metrics:        metricsfake.NewSimpleClientset(),
		Discovery:      &discoveryfake.FakeDiscovery{Fake: &k8stesting.Fake{Resources: res}},
		CurrentContext: "test-context",
	}
	s, err := newStoreFrom(c, apiextfake.NewSimpleClientset())
	if err != nil {
		t.Fatalf("newStoreFrom: %v", err)
	}
	t.Cleanup(s.Close)
	waitLens(t, s)
	return s
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func dynOf(t *testing.T, s *Store) *dynamicfake.FakeDynamicClient {
	t.Helper()
	d, ok := s.c.Dynamic.(*dynamicfake.FakeDynamicClient)
	if !ok {
		t.Fatal("expected a fake dynamic client")
	}
	return d
}

func patchActions(d *dynamicfake.FakeDynamicClient) []k8stesting.PatchActionImpl {
	var out []k8stesting.PatchActionImpl
	for _, a := range d.Actions() {
		if p, ok := a.(k8stesting.PatchActionImpl); ok {
			out = append(out, p)
		}
	}
	return out
}

// A CRD has no patch metadata, so a strategic merge patch is not a legal
// option — every lens write must be a plain JSON merge patch.
func TestLensVerbPatchUsesMergePatch(t *testing.T) {
	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"},
		app("default", "my-app", "OutOfSync", "Healthy"))
	syncStore(t, s, "argocd-apps")

	if _, err := s.LensAction("argocd-apps", "default", "my-app", "argocd-sync", nil); err != nil {
		t.Fatalf("LensAction: %v", err)
	}
	ps := patchActions(dynOf(t, s))
	if len(ps) != 1 {
		t.Fatalf("want exactly 1 patch, got %d", len(ps))
	}
	p := ps[0]
	if p.GetPatchType() != types.MergePatchType {
		t.Errorf("patch type = %v, want MergePatchType", p.GetPatchType())
	}
	if sub := p.GetSubresource(); sub != "" {
		t.Errorf("subresource = %q, want empty for a main-object patch", sub)
	}
	var body map[string]any
	if err := json.Unmarshal(p.GetPatch(), &body); err != nil {
		t.Fatalf("patch body is not JSON: %v", err)
	}
	if _, ok := body["operation"]; !ok {
		t.Errorf("sync must write top-level .operation, got %v", body)
	}
}

// The subresource must be exactly "status". Asserting on the RECORDED action
// rather than the resulting object matters: the fake's tracker ignores the
// subresource when writing, so an object assertion would pass even if the
// patch had gone to the main resource.
func TestLensVerbStatusPatchTargetsTheStatusSubresource(t *testing.T) {
	s := lensStoreWithPack(t, cnpgLikePack, cnpgGVR, "ClusterList", cnpgCluster("data", "my-db"))
	syncStore(t, s, "cl-clusters")

	params := map[string]string{"instance": "my-db-2"}
	if _, err := s.LensAction("cl-clusters", "data", "my-db", "cl-promote", params); err != nil {
		t.Fatalf("LensAction: %v", err)
	}
	ps := patchActions(dynOf(t, s))
	if len(ps) != 1 {
		t.Fatalf("want 1 patch, got %d", len(ps))
	}
	if got := ps[0].GetSubresource(); got != "status" {
		t.Errorf("subresource = %q, want %q", got, "status")
	}
	var body map[string]any
	_ = json.Unmarshal(ps[0].GetPatch(), &body)
	st, _ := body["status"].(map[string]any)
	if st["targetPrimary"] != "my-db-2" {
		t.Errorf("targetPrimary = %v, want the SELECTED instance", st["targetPrimary"])
	}
}

// An annotation whose rendered value is empty must be written as JSON null,
// which REMOVES the key. Writing "" would set the annotation to an empty
// string — a different mutation, and the one that breaks un-fencing.
func TestAnnotationPatchBodyRemovesOnEmptyValue(t *testing.T) {
	body, err := annotationPatchBody(map[string]string{
		"cnpg.io/fencedInstances": "",
		"cnpg.io/reloadedAt":      "{{.Now}}",
	}, lens.Vars{Name: "my-db", Now: "2026-09-14T10:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	ann := m["metadata"].(map[string]any)["annotations"].(map[string]any)
	v, present := ann["cnpg.io/fencedInstances"]
	if !present {
		t.Fatal("the removed key must be PRESENT with a null value, not omitted")
	}
	if v != nil {
		t.Errorf("empty value = %v, want JSON null (removal)", v)
	}
	if ann["cnpg.io/reloadedAt"] != "2026-09-14T10:00:00Z" {
		t.Errorf("templated value = %v", ann["cnpg.io/reloadedAt"])
	}
}

// Preconditions are checked against the cached object before any round trip.
func TestLensActionRefusesWhenAPreconditionHolds(t *testing.T) {
	obj := app("default", "busy-app", "OutOfSync", "Healthy")
	obj.Object["operation"] = map[string]any{"sync": map[string]any{}}

	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"}, obj)
	syncStore(t, s, "argocd-apps")

	_, err := s.LensAction("argocd-apps", "default", "busy-app", "argocd-sync", nil)
	if err == nil {
		t.Fatal("sync must be refused while .operation is set")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("refusal should say why: %v", err)
	}
	if n := len(patchActions(dynOf(t, s))); n != 0 {
		t.Errorf("a refused action still issued %d patches", n)
	}
}

// The refusal must also reach the UI as a disabled button with a reason,
// rather than a button that fails when pressed.
func TestLensActionsReportsDisabledWithAReason(t *testing.T) {
	obj := app("default", "busy-app", "OutOfSync", "Healthy")
	obj.Object["operation"] = map[string]any{"sync": map[string]any{}}
	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"}, obj)
	syncStore(t, s, "argocd-apps")

	var sync domain.LensActionSpec
	for _, sp := range s.LensActions("argocd-apps", "default", "busy-app") {
		if sp.ID == "argocd-sync" {
			sync = sp
		}
	}
	if sync.ID == "" {
		t.Fatal("argocd-sync missing from the pane")
	}
	if !sync.Disabled || sync.DisabledWhy == "" {
		t.Errorf("sync should be disabled with a reason: %+v", sync)
	}
}

// An action needing a value the row cannot supply is OFFERED — it opens a
// form — but refuses to run without one. Disabling it instead would make it
// permanently unreachable, since nothing can fill a form that never opens;
// running it against the row's own name would promote "my-db" instead of
// "my-db-2" and target nothing.
func TestLensActionNeedingAParamIsOfferedButRefusesEmpty(t *testing.T) {
	s := lensStoreWithPack(t, cnpgLikePack, cnpgGVR, "ClusterList", cnpgCluster("data", "my-db"))
	syncStore(t, s, "cl-clusters")

	found := false
	for _, sp := range s.LensActions("cl-clusters", "data", "my-db") {
		if sp.ID != "cl-promote" {
			continue
		}
		found = true
		if sp.Disabled {
			t.Errorf("promote is disabled (%q); it should open a form", sp.DisabledWhy)
		}
		if len(sp.Params) != 1 || sp.Params[0].Name != "instance" {
			t.Errorf("promote params = %+v, want one named instance", sp.Params)
		}
	}
	if !found {
		t.Fatal("cl-promote missing from the pane")
	}
	if _, err := s.LensAction("cl-clusters", "data", "my-db", "cl-promote", nil); err == nil {
		t.Error("firing it with nothing chosen must be refused")
	}
}

// The modal has to show the pack's own disclosure, not a constant in the UI.
func TestLensActionsCarriesTheKubectlLineAndNotice(t *testing.T) {
	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"},
		app("default", "my-app", "OutOfSync", "Healthy"))
	syncStore(t, s, "argocd-apps")

	for _, sp := range s.LensActions("argocd-apps", "default", "my-app") {
		if sp.ID != "argocd-sync" {
			continue
		}
		if !strings.Contains(sp.Kubectl, "kubectl patch applications/my-app -n default") {
			t.Errorf("kubectl line wrong: %q", sp.Kubectl)
		}
		if !strings.Contains(sp.Notice, "argocd-rbac-cm") {
			t.Errorf("notice does not carry the RBAC disclosure: %q", sp.Notice)
		}
		return
	}
	t.Fatal("argocd-sync not found")
}

// A server rejection must arrive verbatim: Kargo's promote refusal names the
// virtual `promote` verb, and that sentence is the whole diagnosis.
func TestLensActionSurfacesServerErrorVerbatim(t *testing.T) {
	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"},
		app("default", "my-app", "OutOfSync", "Healthy"))
	syncStore(t, s, "argocd-apps")

	const msg = `user cannot "promote" on resource "stages"`
	dynOf(t, s).PrependReactor("patch", "applications", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "applications"}, "my-app", errString(msg))
	})

	_, err := s.LensAction("argocd-apps", "default", "my-app", "argocd-sync", nil)
	if err == nil {
		t.Fatal("want the forbidden error")
	}
	if !strings.Contains(err.Error(), msg) {
		t.Errorf("server message was rewritten:\n got: %v\nwant it to contain: %s", err, msg)
	}
}

// The annotate verb must round-trip through a merge patch on metadata.
func TestLensVerbAnnotate(t *testing.T) {
	s := lensStoreWithPack(t, cnpgLikePack, cnpgGVR, "ClusterList", cnpgCluster("data", "my-db"))
	syncStore(t, s, "cl-clusters")

	if _, err := s.LensAction("cl-clusters", "data", "my-db", "cl-unfence", nil); err != nil {
		t.Fatalf("LensAction: %v", err)
	}
	ps := patchActions(dynOf(t, s))
	if len(ps) != 1 {
		t.Fatalf("want 1 patch, got %d", len(ps))
	}
	if ps[0].GetPatchType() != types.MergePatchType {
		t.Errorf("patch type = %v", ps[0].GetPatchType())
	}
	var body map[string]any
	_ = json.Unmarshal(ps[0].GetPatch(), &body)
	ann := body["metadata"].(map[string]any)["annotations"].(map[string]any)
	if v, ok := ann["cl.example.com/fenced"]; !ok || v != nil {
		t.Errorf("unfence must write a null to remove the key, got %v (present=%v)", v, ok)
	}
}

func TestLensAckMatchesAndMismatches(t *testing.T) {
	s := lensStoreWithPack(t, ackPack, ackGVR, "ThingList", ackThing("default", "t1", ""))
	syncStore(t, s, "ack-things")

	ok, err := s.LensAck("ack-things", "default", "t1", "ack-poke", "")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("ack must be false while the controller has not written back")
	}

	// An action with no ack path is done the moment the request returns.
	if ok, _ := s.LensAck("ack-things", "default", "t1", "ack-plain", ""); !ok {
		t.Error("an action with no ack declared must report done")
	}
}

func TestLensAckTrueOnceTheControllerEchoesOurToken(t *testing.T) {
	const token = "2026-09-14T10:00:00Z"
	s := lensStoreWithPack(t, ackPack, ackGVR, "ThingList", ackThing("default", "t1", token))
	syncStore(t, s, "ack-things")

	ok, err := s.LensAck("ack-things", "default", "t1", "ack-poke", token)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("ack must be true once the controller echoes the token we wrote")
	}
}

// The bug this pins: a Stage refreshed at ANY point in the past already
// carries a non-empty .status.lastHandledRefresh. Testing presence would clear
// the spinner instantly, reporting "handled" before the controller had seen
// this request at all.
func TestLensAckIgnoresAPreviousRequestsAcknowledgement(t *testing.T) {
	s := lensStoreWithPack(t, ackPack, ackGVR, "ThingList",
		ackThing("default", "t1", "2026-01-01T00:00:00Z")) // an OLD refresh
	syncStore(t, s, "ack-things")

	ok, err := s.LensAck("ack-things", "default", "t1", "ack-poke", "2026-09-14T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a stale acknowledgement from an earlier request must not satisfy this one")
	}
}

// LensAction hands back the token it wrote, so the caller has something to
// compare against. Without it the ack can only test presence.
func TestLensActionReturnsTheAckToken(t *testing.T) {
	s := lensStoreWithPack(t, ackPack, ackGVR, "ThingList", ackThing("default", "t1", ""))
	syncStore(t, s, "ack-things")

	got, err := s.LensAction("ack-things", "default", "t1", "ack-poke", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("an action declaring ack: must return the token it wrote")
	}
	if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Errorf("token %q is not the RFC3339 timestamp the annotation wrote: %v", got, err)
	}

	// An action with no ack declared has no token to wait on.
	if got, err := s.LensAction("ack-things", "default", "t1", "ack-plain", nil); err != nil || got != "" {
		t.Errorf("ack-plain returned %q, %v — want an empty token", got, err)
	}
}

// target: redirects the WRITE, not just the displayed command. This is the
// whole reason the field exists: Longhorn's detach must patch the
// VolumeAttachment CR, which is named identically to the Volume. Without this
// test, deleting the a.Target branch leaves every other test green while
// lh-detach silently patches the Volume — which Longhorn ignores, so the
// detach looks like it worked and did nothing.
func TestLensActionTargetRedirectsTheWrite(t *testing.T) {
	const targetPack = `
name: targetpack
requires: [tg.example.com/v1]
kinds:
  - key: tg-volumes
    name: Volumes
    short: tgv
    group: TG
    gvr: tg.example.com/v1/volumes
    namespaced: true
    columns: [{header: NAME, path: .metadata.name}]
    actions: [tg-detach]
actions:
  - id: tg-detach
    label: Detach
    verb: patch
    target: tg.example.com/v1/volumeattachments
    patch:
      spec:
        attachmentTickets:
          k10s: null
`
	volGVR := schema.GroupVersionResource{Group: "tg.example.com", Version: "v1", Resource: "volumes"}
	vaGVR := schema.GroupVersionResource{Group: "tg.example.com", Version: "v1", Resource: "volumeattachments"}
	vol := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "tg.example.com/v1",
		"kind":       "Volume",
		"metadata":   map[string]any{"name": "pvc-abc", "namespace": "storage"},
	}}
	// The sibling CR, named identically to the Volume — that naming is the
	// whole reason `target:` works.
	va := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "tg.example.com/v1",
		"kind":       "VolumeAttachment",
		"metadata":   map[string]any{"name": "pvc-abc", "namespace": "storage"},
		"spec": map[string]any{"attachmentTickets": map[string]any{
			"k10s":         map[string]any{"nodeID": "node-1"},
			"csi-attacher": map[string]any{"nodeID": "node-1"},
		}},
	}}
	s := lensStoreWithKinds(t, targetPack, map[schema.GroupVersionResource]string{
		volGVR: "VolumeList",
		vaGVR:  "VolumeAttachmentList",
	}, vol, va)
	syncStore(t, s, "tg-volumes")

	if _, err := s.LensAction("tg-volumes", "storage", "pvc-abc", "tg-detach", nil); err != nil {
		t.Fatalf("LensAction: %v", err)
	}
	ps := patchActions(dynOf(t, s))
	if len(ps) != 1 {
		t.Fatalf("want 1 patch, got %d", len(ps))
	}
	if got := ps[0].GetResource().Resource; got != "volumeattachments" {
		t.Errorf("patch landed on %q, want the TARGET resource volumeattachments", got)
	}
	if got := ps[0].GetName(); got != "pvc-abc" {
		t.Errorf("name = %q — the sibling CR keeps the selected row's name", got)
	}
	if got := ps[0].GetNamespace(); got != "storage" {
		t.Errorf("namespace = %q, want the row's namespace", got)
	}
	// Only our own ticket is cleared; no other key appears in the patch.
	var body map[string]any
	_ = json.Unmarshal(ps[0].GetPatch(), &body)
	tickets := body["spec"].(map[string]any)["attachmentTickets"].(map[string]any)
	if len(tickets) != 1 {
		t.Errorf("patch touches %d tickets, want only k10s's: %v", len(tickets), tickets)
	}
	if v, ok := tickets["k10s"]; !ok || v != nil {
		t.Errorf("k10s ticket = %v (present=%v), want a null that removes it", v, ok)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
