package k8s

import (
	"testing"

	"github.com/0x01001011/k10s/internal/domain"
	corev1 "k8s.io/api/core/v1"
	apiextfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

// A pack whose edges span a TYPED builtin kind and an unstructured lens kind,
// which is the case the reverse index exists for.
const edgePack = `
name: edgepack
requires: [cl.example.com/v1]
kinds:
  - key: cl-clusters
    name: Clusters
    short: clc
    group: CLike
    gvr: cl.example.com/v1/clusters
    namespaced: true
    columns: [{header: NAME, path: .metadata.name}]
  - key: cl-backups
    name: Backups
    short: clb
    group: CLike
    gvr: cl.example.com/v1/backups
    namespaced: true
    columns: [{header: NAME, path: .metadata.name}]
edges:
  - from: v1/pods
    to: cl.example.com/v1/clusters
    via: label
    key: cl.example.com/cluster
  - from: cl.example.com/v1/backups
    to: cl.example.com/v1/clusters
    via: field
    key: .spec.cluster.name
  # An endpoint no kind declares — Longhorn's replicas are the real case.
  - from: cl.example.com/v1/replicas
    to: cl.example.com/v1/clusters
    via: label
    key: cl.example.com/cluster
`

var backupsGVR = schema.GroupVersionResource{Group: "cl.example.com", Version: "v1", Resource: "backups"}

func clusterPod(ns, name, cluster string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: ns,
		Labels:    map[string]string{"cl.example.com/cluster": cluster},
	}}
}

func clBackup(ns, name, cluster string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cl.example.com/v1",
		"kind":       "Backup",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       map[string]any{"cluster": map[string]any{"name": cluster}},
	}}
}

// edgeStore wires typed objects (pods) AND unstructured lens objects together,
// which is what an edge spanning both actually needs.
func edgeStore(t *testing.T, typed []runtime.Object, unstructuredObjs []runtime.Object) *Store {
	t.Helper()
	return edgeStoreWithPack(t, edgePack, typed, unstructuredObjs)
}

// edgeStoreWithPack is edgeStore for a test that needs different edges.
func edgeStoreWithPack(t *testing.T, packYAML string, typed []runtime.Object, unstructuredObjs []runtime.Object) *Store {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "pack.yaml", packYAML)
	t.Setenv("K10S_LENS_DIR", dir)

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			cnpgGVR:    "ClusterList",
			backupsGVR: "BackupList",
		},
		unstructuredObjs...,
	)
	c := &Client{
		RestConfig: &rest.Config{Host: "https://fake"},
		Clientset:  fake.NewSimpleClientset(typed...),
		Dynamic:    dyn,
		Metrics:    metricsfake.NewSimpleClientset(),
		Discovery: &discoveryfake.FakeDiscovery{Fake: &k8stesting.Fake{
			Resources: []*metav1.APIResourceList{{GroupVersion: "cl.example.com/v1"}},
		}},
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

// The hop that motivates the whole feature: land on a pod, walk up to the
// database that owns it — and back down again.
func TestRelatedWalksBothDirections(t *testing.T) {
	s := edgeStore(t,
		[]runtime.Object{clusterPod("data", "my-db-1", "my-db")},
		[]runtime.Object{cnpgCluster("data", "my-db")},
	)
	syncStore(t, s, "cl-clusters")
	syncStore(t, s, "pods")

	// Forward: pod -> cluster, via the pod's label.
	refs, err := s.Related("pods", "data", "my-db-1")
	if err != nil {
		t.Fatalf("Related(pods): %v", err)
	}
	if !hasRef(refs, "cl-clusters", "my-db") {
		t.Errorf("pod did not resolve to its cluster: %+v", refs)
	}

	// Reverse: cluster -> pod, the same declared edge read the other way.
	refs, err = s.Related("cl-clusters", "data", "my-db")
	if err != nil {
		t.Fatalf("Related(cl-clusters): %v", err)
	}
	if !hasRef(refs, "pods", "my-db-1") {
		t.Errorf("cluster did not resolve back to its pod: %+v", refs)
	}
}

// via: field across two lens kinds.
func TestRelatedViaFieldBetweenLensKinds(t *testing.T) {
	s := edgeStore(t, nil, []runtime.Object{
		cnpgCluster("data", "my-db"),
		clBackup("data", "backup-1", "my-db"),
	})
	syncStore(t, s, "cl-clusters")
	syncStore(t, s, "cl-backups")

	refs, err := s.Related("cl-backups", "data", "backup-1")
	if err != nil {
		t.Fatal(err)
	}
	if !hasRef(refs, "cl-clusters", "my-db") {
		t.Errorf("backup did not resolve to its cluster: %+v", refs)
	}

	refs, _ = s.Related("cl-clusters", "data", "my-db")
	if !hasRef(refs, "cl-backups", "backup-1") {
		t.Errorf("cluster did not resolve back to its backup: %+v", refs)
	}
}

// Resolving a relationship must never open a watch behind the user's back.
func TestRelatedUnloadedKindOpensNoWatch(t *testing.T) {
	s := edgeStore(t,
		[]runtime.Object{clusterPod("data", "my-db-1", "my-db")},
		[]runtime.Object{cnpgCluster("data", "my-db")},
	)
	syncStore(t, s, "cl-clusters")
	// pods deliberately NOT opened.

	before := len(dynOf(t, s).Actions())
	refs, err := s.Related("cl-clusters", "data", "my-db")
	if err != nil {
		t.Fatal(err)
	}
	if s.isStarted("pods", "data") {
		t.Error("resolving a relationship started the pods informer")
	}
	if after := len(dynOf(t, s).Actions()); after != before {
		t.Errorf("Related issued %d new dynamic actions", after-before)
	}
	// The edge is reported as unresolved rather than silently dropped.
	found := false
	for _, r := range refs {
		if r.Kind == "pods" && !r.Loaded {
			found = true
		}
	}
	if !found {
		t.Errorf("an edge into an unopened kind must be reported not-loaded: %+v", refs)
	}
}

// A GVR that no kind serves is a different problem from one that is merely
// unopened: opening it would fix the second, never the first.
func TestRelatedKeylessGVRIsReported(t *testing.T) {
	s := edgeStore(t, nil, []runtime.Object{cnpgCluster("data", "my-db")})
	syncStore(t, s, "cl-clusters")

	refs, err := s.Related("cl-clusters", "data", "my-db")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range refs {
		if r.Kind == "cl.example.com/v1/replicas" && !r.Loaded {
			found = true
		}
	}
	if !found {
		t.Errorf("an edge endpoint with no declared kind must be reported by GVR: %+v", refs)
	}
}

// A self-referential edge must terminate. It does so structurally: one hop,
// never a tree, so there is no recursion to bound.
func TestRelatedExcludesSelfAndTerminates(t *testing.T) {
	s := edgeStore(t, nil, []runtime.Object{cnpgCluster("data", "my-db")})
	syncStore(t, s, "cl-clusters")

	refs, err := s.Related("cl-clusters", "data", "my-db")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range refs {
		if r.Kind == "cl-clusters" && r.Name == "my-db" && r.Namespace == "data" {
			t.Errorf("an object appeared in its own relationship list: %+v", r)
		}
	}
}

// A kind with no declared edges is not an error, just an empty panel.
func TestRelatedOnAKindWithNoEdges(t *testing.T) {
	s := edgeStore(t, nil, nil)
	refs, err := s.Related("configmaps", "data", "whatever")
	if err != nil {
		t.Fatalf("a kind with no edges must not error: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("want no refs, got %+v", refs)
	}
}

func TestKindKeyForGVRResolvesBuiltinsAndLensKinds(t *testing.T) {
	s := edgeStore(t, nil, nil)
	cases := map[string]string{
		"v1/pods":                    "pods",
		"v1/persistentvolumeclaims":  "pvcs",
		"apps/v1/deployments":        "deployments",
		"v1/services":                "services",
		"v1/secrets":                 "secrets",
		"cl.example.com/v1/clusters": "cl-clusters",
	}
	for gvr, want := range cases {
		got, ok := s.kindKeyForGVR(gvr)
		if !ok || got != want {
			t.Errorf("kindKeyForGVR(%q) = %q,%v want %q,true", gvr, got, ok, want)
		}
	}
	// An edge endpoint no pack declares as a kind resolves to nothing.
	if got, ok := s.kindKeyForGVR("cl.example.com/v1/replicas"); ok {
		t.Errorf("kindKeyForGVR(replicas) = %q, want no key", got)
	}
}

func hasRef(refs []domain.Ref, kind, name string) bool {
	for _, r := range refs {
		if r.Kind == kind && r.Name == name {
			return true
		}
	}
	return false
}

// A label edge carries a bare value, not a namespace, and is routinely used
// across namespaces: an ArgoCD Application in the argocd namespace labels
// workloads everywhere else. Refusing to cross means the headline hop — pod
// back to the Application that put it there — returns nothing.
func TestRelatedResolvesALabelEdgeAcrossNamespaces(t *testing.T) {
	s := edgeStore(t,
		[]runtime.Object{clusterPod("prod", "my-db-1", "my-db")},
		[]runtime.Object{cnpgCluster("platform", "my-db")},
	)
	syncStore(t, s, "cl-clusters")
	syncStore(t, s, "pods")

	refs, err := s.Related("cl-clusters", "platform", "my-db")
	if err != nil {
		t.Fatalf("Related: %v", err)
	}
	if !hasRef(refs, "pods", "my-db-1") {
		t.Fatalf("a pod in another namespace was dropped: %+v", refs)
	}
	for _, r := range refs {
		if r.Kind == "pods" && r.Name == "my-db-1" && r.Namespace != "prod" {
			t.Errorf("neighbour namespace = %q, want %q — a Ref must carry where the object actually is", r.Namespace, "prod")
		}
	}
}

// An ownerRef cannot cross a namespace; Kubernetes forbids it. Without that
// distinction a Cluster called "my-db" in one namespace collects the pods of
// the unrelated "my-db" in another.
func TestRelatedOwnerRefStaysInItsNamespace(t *testing.T) {
	const ownerPack = `
name: ownerpack
requires: [cl.example.com/v1]
kinds:
  - key: cl-clusters
    name: Clusters
    short: clc
    group: CLike
    gvr: cl.example.com/v1/clusters
    namespaced: true
    columns: [{header: NAME, path: .metadata.name}]
edges:
  - from: v1/pods
    to: cl.example.com/v1/clusters
    via: ownerRef
`
	pod := clusterPod("other", "my-db-1", "my-db")
	pod.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: "cl.example.com/v1", Kind: "Cluster", Name: "my-db",
	}}
	s := edgeStoreWithPack(t, ownerPack,
		[]runtime.Object{pod},
		[]runtime.Object{cnpgCluster("data", "my-db")},
	)
	syncStore(t, s, "cl-clusters")
	syncStore(t, s, "pods")

	refs, err := s.Related("cl-clusters", "data", "my-db")
	if err != nil {
		t.Fatalf("Related: %v", err)
	}
	if hasRef(refs, "pods", "my-db-1") {
		t.Errorf("an ownerRef was followed across a namespace boundary: %+v", refs)
	}
}
