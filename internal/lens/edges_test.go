package lens

import (
	"reflect"
	"sort"
	"testing"
)

func TestEdgesForSplitsBothDirections(t *testing.T) {
	packs, errs := Builtins()
	if len(errs) != 0 {
		t.Fatalf("builtins: %v", errs)
	}

	out, in := EdgesFor(packs, "v1/pods")
	if len(out) == 0 {
		t.Fatal("v1/pods should have outgoing edges (cnpg and argocd both declare one)")
	}
	for _, e := range out {
		if e.From != "v1/pods" {
			t.Errorf("outgoing edge does not start at pods: %+v", e)
		}
	}
	if len(in) != 0 {
		t.Errorf("nothing declares an edge INTO v1/pods, got %+v", in)
	}

	// The same edges, read from the other end.
	_, in2 := EdgesFor(packs, "postgresql.cnpg.io/v1/clusters")
	if len(in2) == 0 {
		t.Fatal("cnpg clusters should have incoming edges")
	}
	found := false
	for _, e := range in2 {
		if e.From == "v1/pods" && e.Via == ViaLabel && e.Key == "cnpg.io/cluster" {
			found = true
		}
	}
	if !found {
		t.Errorf("the pod->cluster edge is not visible from the cluster side: %+v", in2)
	}
}

func TestEdgeValuesPerVia(t *testing.T) {
	cases := []struct {
		name string
		edge Edge
		obj  map[string]any
		want []string
	}{
		{
			name: "label",
			edge: Edge{From: "v1/pods", To: "postgresql.cnpg.io/v1/clusters", Via: ViaLabel, Key: "cnpg.io/cluster"},
			obj: map[string]any{"metadata": map[string]any{
				"name":   "my-db-1",
				"labels": map[string]any{"cnpg.io/cluster": "my-db"},
			}},
			want: []string{"my-db"},
		},
		{
			name: "annotation",
			edge: Edge{From: "a/v1/x", To: "b/v1/y", Via: ViaAnnotation, Key: "kargo.akuity.io/authorized-stage"},
			obj: map[string]any{"metadata": map[string]any{
				"annotations": map[string]any{"kargo.akuity.io/authorized-stage": "prod"},
			}},
			want: []string{"prod"},
		},
		{
			// An object may have several owners, and only those matching the
			// edge's target apiVersion count.
			name: "ownerRef matches only the target apiVersion",
			edge: Edge{From: "v1/pods", To: "apps/v1/replicasets", Via: ViaOwnerRef},
			obj: map[string]any{"metadata": map[string]any{
				"ownerReferences": []any{
					map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "web-abc"},
					map[string]any{"apiVersion": "batch/v1", "kind": "Job", "name": "not-this-one"},
					map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "web-def"},
				},
			}},
			want: []string{"web-abc", "web-def"},
		},
		{
			name: "field",
			edge: Edge{From: "x/v1/backups", To: "x/v1/clusters", Via: ViaField, Key: ".spec.cluster.name"},
			obj:  map[string]any{"spec": map[string]any{"cluster": map[string]any{"name": "my-db"}}},
			want: []string{"my-db"},
		},
		{
			// traefik.yaml's .spec.routes[*].services[*].name genuinely yields
			// several.
			name: "field with a wildcard yields several",
			edge: Edge{From: "traefik.io/v1alpha1/ingressroutes", To: "v1/services", Via: ViaField, Key: ".spec.routes[*].services[*].name"},
			obj: map[string]any{"spec": map[string]any{"routes": []any{
				map[string]any{"services": []any{
					map[string]any{"name": "svc-a"},
					map[string]any{"name": "svc-b"},
				}},
				map[string]any{"services": []any{map[string]any{"name": "svc-c"}}},
			}}},
			want: []string{"svc-a", "svc-b", "svc-c"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Values(tc.edge, tc.obj)
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Values = %v, want %v", got, want)
			}
		})
	}
}

// A miss is an empty result, never a panic. Half the objects a lens walks are
// missing the field an edge names.
func TestEdgeValuesMissIsEmptyNotAPanic(t *testing.T) {
	edges := []Edge{
		{From: "a/v1/x", To: "b/v1/y", Via: ViaLabel, Key: "nope"},
		{From: "a/v1/x", To: "b/v1/y", Via: ViaAnnotation, Key: "nope"},
		{From: "a/v1/x", To: "b/v1/y", Via: ViaOwnerRef},
		{From: "a/v1/x", To: "b/v1/y", Via: ViaField, Key: ".spec.nope.deeper"},
		{From: "a/v1/x", To: "b/v1/y", Via: ViaField, Key: ".status.conditions[0].name"},
	}
	objs := []map[string]any{
		{},
		{"metadata": map[string]any{}},
		{"metadata": map[string]any{"labels": map[string]any{}, "ownerReferences": []any{}}},
		{"status": map[string]any{"conditions": []any{}}},
	}
	for _, e := range edges {
		for _, o := range objs {
			if got := Values(e, o); len(got) != 0 {
				t.Errorf("Values(%s, %v) = %v, want nothing", e.Via, o, got)
			}
		}
	}
}

// An unknown via yields nothing rather than guessing. Validation rejects these
// at parse time, so this only guards a future via added to the constants but
// not to the switch.
func TestEdgeValuesUnknownViaYieldsNothing(t *testing.T) {
	if got := Values(Edge{Via: "telepathy", Key: "x"}, map[string]any{}); got != nil {
		t.Errorf("unknown via returned %v", got)
	}
}

// Every edge endpoint in the shipped packs must parse as a GVR. A typo here
// would silently disable that hop rather than failing loudly.
func TestShippedEdgeEndpointsAreWellFormed(t *testing.T) {
	packs, _ := Builtins()
	for _, p := range packs {
		for _, e := range p.Edges {
			for _, gvr := range []string{e.From, e.To} {
				if _, _, _, err := ParseGVR(gvr); err != nil {
					t.Errorf("lens %q edge %s->%s: %q: %v", p.Name, e.From, e.To, gvr, err)
				}
			}
		}
	}
}
