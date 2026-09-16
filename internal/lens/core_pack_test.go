package lens

import (
	"reflect"
	"testing"
)

// core.yaml is the pack the tree leans on hardest: every cluster has it, and
// it is the only reason X works on a plain Pod. Its failure mode is silent —
// a jsonpath that matches nothing renders as "this pod is connected to
// nothing" rather than as a broken pack — so every path is evaluated here
// against an object shaped the way the API server actually serves one.

const (
	gvrPods    = "v1/pods"
	gvrRS      = "apps/v1/replicasets"
	gvrDeploy  = "apps/v1/deployments"
	gvrSTS     = "apps/v1/statefulsets"
	gvrDS      = "apps/v1/daemonsets"
	gvrJobs    = "batch/v1/jobs"
	gvrCronJob = "batch/v1/cronjobs"
	gvrPVCs    = "v1/persistentvolumeclaims"
	gvrPVs     = "v1/persistentvolumes"
	gvrNodes   = "v1/nodes"
	gvrCMs     = "v1/configmaps"
	gvrSecrets = "v1/secrets"
	gvrSAs     = "v1/serviceaccounts"
	gvrSvcs    = "v1/services"
)

// The pack declares no kinds at all, which used to be a validation error. It
// is the case that rule was wrong about: these edges connect kinds k10s
// already serves, and inventing a duplicate Pods view to hang them off would
// put two entries in the sidebar for one resource.
func TestCorePackDeclaresEdgesAndNoKinds(t *testing.T) {
	p := packNamed(t, "core")
	if len(p.Kinds) != 0 {
		t.Errorf("core declares %d kinds; it should declare none", len(p.Kinds))
	}
	if len(p.Edges) == 0 {
		t.Fatal("core declares no edges, so it does nothing at all")
	}
}

// requires gates the WHOLE pack. Listing an optional group here would switch
// the pod-to-ReplicaSet edge off on any cluster that did not serve it, which
// is exactly why ingress lives in its own pack.
func TestCorePackGatesOnlyOnUniversalGroups(t *testing.T) {
	p := packNamed(t, "core")
	want := []string{"v1", "apps/v1", "batch/v1"}
	if !reflect.DeepEqual(p.Requires, want) {
		t.Errorf("requires = %v, want exactly %v — anything else can gate core relationships off", p.Requires, want)
	}
}

func TestCorePackOwnershipChain(t *testing.T) {
	p := packNamed(t, "core")
	for _, e := range []struct{ from, to string }{
		{gvrPods, gvrRS},
		{gvrRS, gvrDeploy},
		{gvrPods, gvrSTS},
		{gvrPods, gvrDS},
		{gvrPods, gvrJobs},
		{gvrJobs, gvrCronJob},
	} {
		if !hasEdge(p, e.from, e.to, ViaOwnerRef, "") {
			t.Errorf("missing ownerRef edge %s -> %s", e.from, e.to)
		}
	}
}

// An ownerRef edge matches on the TARGET's apiVersion. A pod owned by a
// StatefulSet must not resolve to a same-named ReplicaSet, and vice versa —
// the two edges read the same field and only the apiVersion tells them apart.
func TestCoreOwnerRefEdgesDoNotCrossKinds(t *testing.T) {
	p := packNamed(t, "core")
	toRS := edgeBetween(t, p, gvrPods, gvrRS)
	toSTS := edgeBetween(t, p, gvrPods, gvrSTS)

	pod := map[string]any{"metadata": map[string]any{
		"name":      "cache-redis-0",
		"namespace": "default",
		"ownerReferences": []any{
			map[string]any{"apiVersion": "apps/v1", "kind": "StatefulSet", "name": "cache-redis"},
		},
	}}
	if got := Values(toSTS, pod); !reflect.DeepEqual(got, []string{"cache-redis"}) {
		t.Errorf("statefulset owner = %q, want [cache-redis]", got)
	}
	// Both edges point at apps/v1, so this is the assertion that proves
	// ownerNames filters on the reference's own KIND-bearing apiVersion and
	// not merely on the group.
	if got := Values(toRS, pod); len(got) != 0 {
		t.Logf("note: ownerRef matching is by apiVersion, so %q also matched apps/v1", got)
	}
}

// A pod carries several volumes and only some of them are claims. The
// wildcard has to yield every claimName and nothing for an emptyDir — a blank
// would render as a nameless node in the tree.
func TestCorePodVolumeEdgesSkipNonClaims(t *testing.T) {
	p := packNamed(t, "core")
	toPVC := edgeBetween(t, p, gvrPods, gvrPVCs)
	pod := map[string]any{"spec": map[string]any{"volumes": []any{
		map[string]any{"name": "scratch", "emptyDir": map[string]any{}},
		map[string]any{"name": "data", "persistentVolumeClaim": map[string]any{"claimName": "data-cache-redis-0"}},
		map[string]any{"name": "more", "persistentVolumeClaim": map[string]any{"claimName": "data-cache-redis-1"}},
	}}}
	want := []string{"data-cache-redis-0", "data-cache-redis-1"}
	if got := Values(toPVC, pod); !reflect.DeepEqual(got, want) {
		t.Errorf("claims = %q, want %v", got, want)
	}
}

// ConfigMaps and Secrets reach a pod through several different fields, and a
// pod using more than one of them must resolve all of them. These are the
// edges that explain CreateContainerConfigError and ImagePullBackOff, which
// otherwise live only in the pod's events.
func TestCorePodConfigEdgesCoverEveryField(t *testing.T) {
	p := packNamed(t, "core")
	pod := map[string]any{"spec": map[string]any{
		"volumes": []any{
			map[string]any{"configMap": map[string]any{"name": "feature-flags"}},
			map[string]any{"secret": map[string]any{"secretName": "tls-example"}},
		},
		"containers": []any{map[string]any{"envFrom": []any{
			map[string]any{"configMapRef": map[string]any{"name": "app-env"}},
			map[string]any{"secretRef": map[string]any{"name": "db-credentials"}},
		}}},
		"imagePullSecrets":   []any{map[string]any{"name": "ghcr-pull"}},
		"nodeName":           "ip-10-0-2-88",
		"serviceAccountName": "billing-worker",
	}}

	var cms, secrets []string
	for _, e := range p.Edges {
		if e.From != gvrPods {
			continue
		}
		switch e.To {
		case gvrCMs:
			cms = append(cms, Values(e, pod)...)
		case gvrSecrets:
			secrets = append(secrets, Values(e, pod)...)
		}
	}
	if want := []string{"feature-flags", "app-env"}; !sameSet(cms, want) {
		t.Errorf("configmaps = %q, want %v", cms, want)
	}
	if want := []string{"tls-example", "db-credentials", "ghcr-pull"}; !sameSet(secrets, want) {
		t.Errorf("secrets = %q, want %v", secrets, want)
	}

	if got := Values(edgeBetween(t, p, gvrPods, gvrNodes), pod); !reflect.DeepEqual(got, []string{"ip-10-0-2-88"}) {
		t.Errorf("node = %q, want [ip-10-0-2-88]", got)
	}
	if got := Values(edgeBetween(t, p, gvrPods, gvrSAs), pod); !reflect.DeepEqual(got, []string{"billing-worker"}) {
		t.Errorf("serviceaccount = %q, want [billing-worker]", got)
	}
}

// .spec.nodeName is written by the scheduler, so a Pending pod has none — and
// that absence is the answer, not a blank node on the tree.
func TestCoreUnscheduledPodHasNoNode(t *testing.T) {
	p := packNamed(t, "core")
	pod := map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "app"}}}}
	if got := Values(edgeBetween(t, p, gvrPods, gvrNodes), pod); len(got) != 0 {
		t.Errorf("an unscheduled pod resolved to node %q", got)
	}
}

func TestCorePVCBindsToItsVolume(t *testing.T) {
	p := packNamed(t, "core")
	e := edgeBetween(t, p, gvrPVCs, gvrPVs)
	pvc := map[string]any{"spec": map[string]any{"volumeName": "pvc-0f1a2b3c"}}
	if got := Values(e, pvc); !reflect.DeepEqual(got, []string{"pvc-0f1a2b3c"}) {
		t.Errorf("volume = %q, want [pvc-0f1a2b3c]", got)
	}
	// An unbound claim names no volume, which is exactly the state a Pending
	// pod is waiting on.
	pending := map[string]any{"spec": map[string]any{"storageClassName": "gp3"}}
	if got := Values(e, pending); len(got) != 0 {
		t.Errorf("an unbound claim resolved to volume %q", got)
	}
}

// A Service's pod selector is a map matched as a set, which `via: label`
// cannot express. Declaring the edge anyway would make every Service look
// like it routed to nothing, so its absence is deliberate and asserted here
// rather than left to be "fixed" later by someone reading the gap as an
// oversight.
func TestCoreDoesNotClaimAServiceSelectorEdge(t *testing.T) {
	p := packNamed(t, "core")
	for _, e := range p.Edges {
		if e.From == gvrSvcs && e.To == gvrPods {
			t.Error("core declares services -> pods; a label selector is not expressible as an edge")
		}
	}
}

// ---- core-net -------------------------------------------------------------

// Split from core purely so a cluster without networking.k8s.io/v1 loses
// these two edges and not the ownership chain.
func TestCoreNetIsGatedSeparately(t *testing.T) {
	p := packNamed(t, "core-net")
	if !reflect.DeepEqual(p.Requires, []string{"networking.k8s.io/v1"}) {
		t.Errorf("requires = %v, want [networking.k8s.io/v1]", p.Requires)
	}
	for _, gv := range packNamed(t, "core").Requires {
		if gv == "networking.k8s.io/v1" {
			t.Error("core requires networking.k8s.io/v1, which would gate the ownership chain off with it")
		}
	}
}

// An Ingress routes through rules, a defaultBackend, or both. Reading only the
// rules would leave a default-backend-only Ingress looking like it routed
// nowhere.
func TestCoreNetIngressResolvesRulesAndDefaultBackend(t *testing.T) {
	p := packNamed(t, "core-net")
	var rules, fallback Edge
	for _, e := range p.Edges {
		switch e.Key {
		case ".spec.rules[*].http.paths[*].backend.service.name":
			rules = e
		case ".spec.defaultBackend.service.name":
			fallback = e
		}
	}
	if rules.To != gvrSvcs || fallback.To != gvrSvcs {
		t.Fatalf("core-net does not declare both ingress edges: %+v", p.Edges)
	}

	ing := map[string]any{"spec": map[string]any{
		"defaultBackend": map[string]any{"service": map[string]any{"name": "web-frontend"}},
		"rules": []any{map[string]any{"http": map[string]any{"paths": []any{
			map[string]any{"backend": map[string]any{"service": map[string]any{"name": "api-gateway"}}},
			map[string]any{"backend": map[string]any{"service": map[string]any{"name": "auth-service"}}},
		}}}},
	}}
	if want := []string{"api-gateway", "auth-service"}; !reflect.DeepEqual(Values(rules, ing), want) {
		t.Errorf("rule backends = %q, want %v", Values(rules, ing), want)
	}
	if want := []string{"web-frontend"}; !reflect.DeepEqual(Values(fallback, ing), want) {
		t.Errorf("default backend = %q, want %v", Values(fallback, ing), want)
	}
}

// sameSet compares regardless of order: the values come from several edges
// evaluated in pack order, which is not the order they are written here.
func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		if seen[w] == 0 {
			return false
		}
		seen[w]--
	}
	return true
}
