package lens

import (
	"strings"
	"testing"
)

// k3sUpgradePack is the SHIPPED pack, not a literal: the point of these
// tests is that the file in internal/lens/builtin actually parses.
func k3sUpgradePack(t *testing.T) Pack {
	t.Helper()
	packs, errs := Builtins()
	for _, err := range errs {
		t.Fatalf("builtin packs failed to parse: %v", err)
	}
	for _, p := range packs {
		if p.Name == "k3s-upgrade" {
			return p
		}
	}
	t.Fatalf("builtin pack %q not found", "k3s-upgrade")
	return Pack{}
}

// Exact headers, not a contains check: a dropped column is precisely the
// bug this is here to catch, and an empty column reads as a k10s bug.
func TestK3sUpgradePackColumns(t *testing.T) {
	p := k3sUpgradePack(t)

	want := map[string]string{
		"k3s-upgrade-plans": "NAME,CHANNEL,LATEST,RESOLVED,COMPLETE,APPLYING,MESSAGE,AGE",
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
	}
}

// One collision drops the WHOLE pack at registry build, actions and edges
// included. Keys and shorts share one flat namespace across every builtin.
func TestK3sUpgradePackDoesNotCollide(t *testing.T) {
	packs, _ := Builtins()
	taken := map[string]string{}
	for _, p := range packs {
		if p.Name == "k3s-upgrade" {
			continue
		}
		for _, k := range p.Kinds {
			taken[k.Key] = p.Name
			if k.Short != "" {
				taken[k.Short] = p.Name
			}
		}
	}
	// The builtin k8s kinds are not visible from this package; these are
	// the ones a "plan"-flavoured short could plausibly hit.
	for _, s := range []string{"pods", "po", "jobs", "job", "nodes", "no", "cronjobs", "cj"} {
		taken[s] = "k8s builtin"
	}
	for _, k := range k3sUpgradePack(t).Kinds {
		for _, name := range []string{k.Key, k.Short} {
			if name == "" {
				continue
			}
			if owner, clash := taken[name]; clash {
				t.Errorf("%q is already taken by %s — the whole pack would be dropped", name, owner)
			}
			taken[name] = "k3s-upgrade"
		}
	}
}

// Both discovery gates read this data: gate 1 needs `requires` to name the
// group/version the kinds actually live at, gate 2 needs every gvr's
// resource plural. A requires that names some other group/version gates the
// pack off on the very clusters that serve it.
func TestK3sUpgradePackRequiresMatchesItsKinds(t *testing.T) {
	p := k3sUpgradePack(t)
	if len(p.Requires) == 0 {
		t.Fatal("pack declares no requires — it would appear on every cluster")
	}
	req := map[string]bool{}
	for _, r := range p.Requires {
		req[r] = true
	}
	for _, k := range p.Kinds {
		group, version, resource, err := ParseGVR(k.GVR)
		if err != nil {
			t.Fatalf("kind %q has an unparseable gvr %q: %v", k.Key, k.GVR, err)
		}
		if gv := group + "/" + version; !req[gv] {
			t.Errorf("kind %q lives at %s, which requires does not list %v", k.Key, gv, p.Requires)
		}
		if resource != strings.ToLower(resource) {
			t.Errorf("kind %q resource %q must be the lowercase plural discovery reports", k.Key, resource)
		}
	}
}

// Every severity a column names must exist, or the pack fails to parse at
// all; and every edge endpoint must be a real GVR or the hop silently never
// resolves, which reads as "nothing is connected".
func TestK3sUpgradePackEdgesAndSeverities(t *testing.T) {
	p := k3sUpgradePack(t)
	for _, k := range p.Kinds {
		for _, c := range k.Columns {
			if c.Severity == "" {
				continue
			}
			if _, ok := p.Severities[c.Severity]; !ok {
				t.Errorf("column %q names severity table %q, which the pack does not declare", c.Header, c.Severity)
			}
		}
	}
	if len(p.Edges) != 2 {
		t.Fatalf("pack declares %d edges, want 2 (Plan->Job, Job->Node)", len(p.Edges))
	}
	for _, e := range p.Edges {
		for _, gvr := range []string{e.From, e.To} {
			if _, _, _, err := ParseGVR(gvr); err != nil {
				t.Errorf("edge endpoint %q does not parse: %v", gvr, err)
			}
		}
		if e.Via != "label" || e.Key == "" {
			t.Errorf("edge %s->%s: want a verified label key, got via=%q key=%q", e.From, e.To, e.Via, e.Key)
		}
	}
}
