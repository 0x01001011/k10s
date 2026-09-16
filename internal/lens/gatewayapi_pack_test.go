package lens

import (
	"strings"
	"testing"
)

// gwPack is the SHIPPED pack, not a literal: these tests exist to prove that
// the file in builtin/ parses and carries the columns we think it carries.
func gwPack(t *testing.T) Pack {
	t.Helper()
	packs, errs := Builtins()
	for _, err := range errs {
		t.Fatalf("builtin packs failed to parse: %v", err)
	}
	for _, p := range packs {
		if p.Name == "gatewayapi" {
			return p
		}
	}
	t.Fatal("builtin pack \"gatewayapi\" not found")
	return Pack{}
}

// Exact headers, not a contains check: a dropped column renders empty on every
// row forever and reads as a k10s bug rather than a field that does not exist.
func TestGatewayAPIPackColumns(t *testing.T) {
	p := gwPack(t)

	want := map[string]string{
		"gw-classes":    "NAME,CONTROLLER,ACCEPTED,AGE",
		"gw-gateways":   "NAME,CLASS,ADDRESS,PORTS,LISTENERS,ATTACHED,PROGRAMMED,ACCEPTED,AGE",
		"gw-httproutes": "NAME,HOSTNAMES,PARENTS,ACCEPTED,RESOLVEDREFS,AGE",
		"gw-grpcroutes": "NAME,HOSTNAMES,PARENTS,ACCEPTED,RESOLVEDREFS,AGE",
	}
	// Only GatewayClass is scope=Cluster upstream (gatewayclass_types.go,
	// +kubebuilder:resource:scope=Cluster); the other three are Namespaced,
	// and getting this wrong leaks every namespace into the view.
	namespaced := map[string]bool{
		"gw-classes": false, "gw-gateways": true,
		"gw-httproutes": true, "gw-grpcroutes": true,
	}
	if len(p.Kinds) != len(want) {
		t.Fatalf("pack declares %d kinds, want %d", len(p.Kinds), len(want))
	}
	for _, k := range p.Kinds {
		cols, ok := want[k.Key]
		if !ok {
			t.Fatalf("unexpected kind %q", k.Key)
		}
		var got []string
		for _, c := range k.Columns {
			got = append(got, c.Header)
		}
		if j := strings.Join(got, ","); j != cols {
			t.Errorf("%s headers = %q, want %q", k.Key, j, cols)
		}
		if k.Namespaced != namespaced[k.Key] {
			t.Errorf("%s namespaced = %v, want %v", k.Key, k.Namespaced, namespaced[k.Key])
		}
	}
}

// The discovery gate is group/version AND resource, for EVERY kind: one wrong
// plural hides the whole pack on clusters that do run Gateway API.
func TestGatewayAPIPackGatesOnEveryKind(t *testing.T) {
	p := gwPack(t)
	if len(p.Requires) != 1 || p.Requires[0] != "gateway.networking.k8s.io/v1" {
		t.Fatalf("requires = %v, want exactly [gateway.networking.k8s.io/v1]", p.Requires)
	}
	plural := map[string]string{
		"gw-classes":    "gatewayclasses",
		"gw-gateways":   "gateways",
		"gw-httproutes": "httproutes",
		"gw-grpcroutes": "grpcroutes",
	}
	for _, k := range p.Kinds {
		g, v, r, err := ParseGVR(k.GVR)
		if err != nil {
			t.Fatalf("%s: bad gvr %q: %v", k.Key, k.GVR, err)
		}
		if g+"/"+v != p.Requires[0] {
			t.Errorf("%s lives at %s/%s, outside requires %v", k.Key, g, v, p.Requires)
		}
		if r != plural[k.Key] {
			t.Errorf("%s resource = %q, want %q", k.Key, r, plural[k.Key])
		}
	}
}

// Keys and shorts share ONE flat namespace; a single collision drops the whole
// pack silently at registry build time.
func TestGatewayAPIPackNamesAreFree(t *testing.T) {
	p := gwPack(t)
	taken := map[string]string{}
	for _, n := range strings.Fields(`
		pods deployments replicasets statefulsets daemonsets jobs cronjobs hpas services
		endpoints ingresses networkpolicies configmaps secrets resourcequotas limitranges
		pdbs pvcs pvs storageclasses serviceaccounts roles rolebindings clusterroles
		clusterrolebindings nodes namespaces events crds customresources
		po deploy rs sts ds job cj hpa svc ep ing netpol cm sec quota limits pdb pvc pv
		sc sa role rb crole crb no ns ev crd cr`) {
		taken[n] = "k8s builtin"
	}
	packs, _ := Builtins()
	for _, other := range packs {
		if other.Name == p.Name {
			continue
		}
		for _, k := range other.Kinds {
			taken[k.Key] = "pack " + other.Name
			if k.Short != "" {
				taken[k.Short] = "pack " + other.Name
			}
		}
	}
	for _, k := range p.Kinds {
		for _, n := range []string{k.Key, k.Short} {
			if n == "" {
				t.Errorf("kind %q has no short alias", k.Key)
				continue
			}
			if owner, dup := taken[n]; dup {
				t.Errorf("%q is already claimed by %s — the gatewayapi pack would be dropped entirely", n, owner)
			}
			taken[n] = "pack " + p.Name
		}
	}
}

// Every Gateway API condition we grade is True on a healthy object, so False
// must sort to the top and an absent condition must stay ungraded.
func TestGatewayAPIPackSeverity(t *testing.T) {
	p := gwPack(t)
	sev, ok := p.Severities["condition"]
	if !ok {
		t.Fatal("severity table \"condition\" missing")
	}
	for value, want := range map[string]Level{
		"True": LevelOK, "False": LevelError, "Unknown": LevelWarn, "": LevelUnknown,
	} {
		if got := sev.Level(value); got != want {
			t.Errorf("%q graded %v, want %v", value, got, want)
		}
	}
	for _, k := range p.Kinds {
		for _, c := range k.Columns {
			if c.Severity != "" && c.Severity != "condition" {
				t.Errorf("%s/%s grades with %q, which the pack does not declare", k.Key, c.Header, c.Severity)
			}
		}
	}
}

// Both edges are name-carrying spec fields; via: field is the only form that
// can express them, and the key must stay a parseable JSONPath.
func TestGatewayAPIPackEdges(t *testing.T) {
	p := gwPack(t)
	want := map[string]string{
		"gateway.networking.k8s.io/v1/httproutes": ".spec.parentRefs[*].name",
		"gateway.networking.k8s.io/v1/grpcroutes": ".spec.parentRefs[*].name",
		"gateway.networking.k8s.io/v1/gateways":   ".spec.gatewayClassName",
	}
	if len(p.Edges) != len(want) {
		t.Fatalf("pack declares %d edges, want %d", len(p.Edges), len(want))
	}
	for _, e := range p.Edges {
		key, ok := want[e.From]
		if !ok {
			t.Fatalf("unexpected edge from %q", e.From)
		}
		if e.Via != ViaField || e.Key != key {
			t.Errorf("edge %s -> %s: via=%q key=%q, want via=field key=%q", e.From, e.To, e.Via, e.Key, key)
		}
	}
}
