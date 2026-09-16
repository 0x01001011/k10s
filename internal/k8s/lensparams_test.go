package k8s

import (
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/0x01001011/k10s/internal/domain"
)

// paramLikePack mirrors what the CNPG pack will declare, written here so a
// pack edit cannot break the mechanism's own tests.
const paramLikePack = `
name: paramlike
requires: [pl.example.com/v1]
kinds:
  - key: pl-clusters
    name: Clusters
    short: plc
    group: PLike
    gvr: pl.example.com/v1/clusters
    namespaced: true
    columns:
      - {header: NAME, path: .metadata.name}
    actions: [pl-fence, pl-backup]
actions:
  - id: pl-fence
    label: Fence instance
    verb: annotate
    confirm: typed
    confirmValue: "{{.Params.instance}}"
    params:
      - name: instance
        label: instance
        required: true
        optionsFrom: .status.instanceNames
      - name: role
        label: role
        optionsFrom: .status.instancesReportedState
    annotations:
      pl.example.com/fenced: '["{{.Params.instance}}"]'
  - id: pl-backup
    label: Back up now
    verb: create
    params:
      - name: method
        label: method
        default: barmanObjectStore
        options:
          - {value: barmanObjectStore}
          - {value: volumeSnapshot}
    template:
      apiVersion: pl.example.com/v1
      kind: Backup
      metadata:
        generateName: "{{.Name}}-k10s-"
        namespace: "{{.Namespace}}"
      spec:
        cluster: {name: "{{.Name}}"}
        method: "{{.Params.method}}"
`

var plGVR = schema.GroupVersionResource{Group: "pl.example.com", Version: "v1", Resource: "clusters"}

func plCluster(ns, name string, instances ...string) *unstructured.Unstructured {
	names := make([]any, 0, len(instances))
	reported := map[string]any{}
	for i, in := range instances {
		names = append(names, in)
		reported[in] = map[string]any{"isPrimary": i == 0, "timeLineID": int64(1)}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "pl.example.com/v1",
		"kind":       "Cluster",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"status": map[string]any{
			"instanceNames":          names,
			"instancesReportedState": reported,
		},
	}}
}

func paramSpec(t *testing.T, specs []domain.LensActionSpec, action, param string) domain.LensParamSpec {
	t.Helper()
	for _, sp := range specs {
		if sp.ID != action {
			continue
		}
		for _, p := range sp.Params {
			if p.Name == param {
				return p
			}
		}
		t.Fatalf("action %q has no param %q (has %+v)", action, param, sp.Params)
	}
	t.Fatalf("action %q missing from the pane", action)
	return domain.LensParamSpec{}
}

func optionValues(p domain.LensParamSpec) []string {
	out := make([]string, 0, len(p.Options))
	for _, o := range p.Options {
		out = append(out, o.Value)
	}
	return out
}

// The point of the whole mechanism: the operator picks from what the cluster
// actually has, rather than typing a pod name from memory into a free-text box.
func TestLensParamsResolveAListFromTheObject(t *testing.T) {
	s := lensStoreWithPack(t, paramLikePack, plGVR, "ClusterList",
		plCluster("data", "my-db", "my-db-1", "my-db-2", "my-db-3"))
	syncStore(t, s, "pl-clusters")

	p := paramSpec(t, s.LensActions("pl-clusters", "data", "my-db", ""), "pl-fence", "instance")
	if got := strings.Join(optionValues(p), ","); got != "my-db-1,my-db-2,my-db-3" {
		t.Errorf("instance options = %q, want the cluster's three instances", got)
	}
	if !p.Required {
		t.Error("instance should carry the pack's required flag through to the UI")
	}
}

// A map source yields its KEYS, sorted. instancesReportedState is a map keyed
// by pod name, and sorting it is what stops the list reshuffling between
// frames for no reason the operator can see.
func TestLensParamsResolveAMapsKeysInOrder(t *testing.T) {
	s := lensStoreWithPack(t, paramLikePack, plGVR, "ClusterList",
		plCluster("data", "my-db", "my-db-2", "my-db-1"))
	syncStore(t, s, "pl-clusters")

	p := paramSpec(t, s.LensActions("pl-clusters", "data", "my-db", ""), "pl-fence", "role")
	if got := strings.Join(optionValues(p), ","); got != "my-db-1,my-db-2" {
		t.Errorf("role options = %q, want the map's keys in sorted order", got)
	}
}

// A path that resolves to nothing is not an error. A cluster mid-bootstrap has
// no instanceNames yet, and refusing to describe the action would be a worse
// answer than describing it with an empty list.
func TestLensParamsTolerateAMissingPath(t *testing.T) {
	obj := plCluster("data", "my-db")
	unstructured.RemoveNestedField(obj.Object, "status", "instanceNames")

	s := lensStoreWithPack(t, paramLikePack, plGVR, "ClusterList", obj)
	syncStore(t, s, "pl-clusters")

	p := paramSpec(t, s.LensActions("pl-clusters", "data", "my-db", ""), "pl-fence", "instance")
	if len(p.Options) != 0 {
		t.Errorf("options = %+v, want none", p.Options)
	}
}

// A fixed list declared in the pack needs no cluster at all, and must survive
// the same trip with its default intact.
func TestLensParamsCarryAFixedListAndItsDefault(t *testing.T) {
	s := lensStoreWithPack(t, paramLikePack, plGVR, "ClusterList", plCluster("data", "my-db", "my-db-1"))
	syncStore(t, s, "pl-clusters")

	p := paramSpec(t, s.LensActions("pl-clusters", "data", "my-db", ""), "pl-backup", "method")
	if got := strings.Join(optionValues(p), ","); got != "barmanObjectStore,volumeSnapshot" {
		t.Errorf("method options = %q", got)
	}
	if p.Default != "barmanObjectStore" {
		t.Errorf("default = %q", p.Default)
	}
}

// The write has to carry what the operator chose. Everything else in this file
// is preparation for this one assertion.
func TestLensActionWritesTheChosenParameter(t *testing.T) {
	s := lensStoreWithPack(t, paramLikePack, plGVR, "ClusterList",
		plCluster("data", "my-db", "my-db-1", "my-db-2"))
	syncStore(t, s, "pl-clusters")

	if _, err := s.LensAction("pl-clusters", "data", "my-db", "pl-fence", "",
		map[string]string{"instance": "my-db-2"}); err != nil {
		t.Fatalf("LensAction: %v", err)
	}

	patches := patchActions(dynOf(t, s))
	if len(patches) != 1 {
		t.Fatalf("issued %d patches, want 1", len(patches))
	}
	var body map[string]any
	if err := json.Unmarshal(patches[0].GetPatch(), &body); err != nil {
		t.Fatalf("patch is not JSON: %v", err)
	}
	ann := body["metadata"].(map[string]any)["annotations"].(map[string]any)
	if got := ann["pl.example.com/fenced"]; got != `["my-db-2"]` {
		t.Errorf("fenced = %v, want the chosen instance", got)
	}
}

// A required parameter with no value must be refused BEFORE the round trip.
// Annotating with an empty instance list is a request the server accepts and
// that fences nothing — success-shaped, and a lie.
func TestLensActionRefusesAnEmptyRequiredParameter(t *testing.T) {
	s := lensStoreWithPack(t, paramLikePack, plGVR, "ClusterList",
		plCluster("data", "my-db", "my-db-1"))
	syncStore(t, s, "pl-clusters")

	_, err := s.LensAction("pl-clusters", "data", "my-db", "pl-fence", "", nil)
	if err == nil {
		t.Fatal("firing with no instance must be refused")
	}
	if !strings.Contains(err.Error(), "instance") {
		t.Errorf("refusal should name the parameter: %v", err)
	}
	if n := len(patchActions(dynOf(t, s))); n != 0 {
		t.Errorf("a refused action still issued %d patches", n)
	}
}

// The preview is what the operator checks the button against, so it has to be
// recomputable from the parameters currently in the form.
func TestLensPreviewReflectsTheCurrentParameters(t *testing.T) {
	s := lensStoreWithPack(t, paramLikePack, plGVR, "ClusterList",
		plCluster("data", "my-db", "my-db-1", "my-db-2"))
	syncStore(t, s, "pl-clusters")

	got := s.LensPreview("pl-clusters", "data", "my-db", "pl-fence", "",
		map[string]string{"instance": "my-db-2"})
	if !strings.Contains(got, `["my-db-2"]`) {
		t.Errorf("preview = %q, want the chosen instance", got)
	}

	got = s.LensPreview("pl-clusters", "data", "my-db", "pl-backup", "",
		map[string]string{"method": "volumeSnapshot"})
	if !strings.Contains(got, "volumeSnapshot") {
		t.Errorf("preview = %q, want the chosen method", got)
	}
}

// confirmValue names the string a typed confirmation must match. Fencing is
// about an INSTANCE; asking the operator to type the cluster's name confirms
// something they were never shown, which is the failure typed-confirm exists
// to prevent.
func TestLensActionsCarryTheConfirmValue(t *testing.T) {
	s := lensStoreWithPack(t, paramLikePack, plGVR, "ClusterList",
		plCluster("data", "my-db", "my-db-1", "my-db-2"))
	syncStore(t, s, "pl-clusters")

	for _, sp := range s.LensActions("pl-clusters", "data", "my-db", "") {
		if sp.ID != "pl-fence" {
			continue
		}
		if sp.ConfirmValue != "{{.Params.instance}}" {
			t.Errorf("confirmValue = %q, want the instance template", sp.ConfirmValue)
		}
		return
	}
	t.Fatal("pl-fence missing from the pane")
}
