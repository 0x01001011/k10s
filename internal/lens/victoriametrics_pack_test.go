package lens

import (
	"strings"
	"testing"
)

// vmPack is the SHIPPED pack, not a literal: these tests exist to prove that
// the file in builtin/ parses and carries the columns we think it carries.
func vmPack(t *testing.T) Pack {
	t.Helper()
	packs, errs := Builtins()
	for _, err := range errs {
		t.Fatalf("builtin packs failed to parse: %v", err)
	}
	for _, p := range packs {
		if p.Name == "victoriametrics" {
			return p
		}
	}
	t.Fatal("builtin pack \"victoriametrics\" not found")
	return Pack{}
}

// Exact headers, not a contains check: a dropped column is precisely the bug
// this catches, because an unresolvable path renders an empty cell on every
// row and reads as a k10s bug rather than as a field that does not exist.
func TestVMPackColumns(t *testing.T) {
	p := vmPack(t)

	want := map[string]string{
		"vm-agents":         "NAME,STATUS,SHARDS,REPLICAS,PAUSED,AGE",
		"vm-alerts":         "NAME,STATUS,REPLICAS,PAUSED,AGE",
		"vm-rules":          "NAME,STATUS,GROUPS,REASON,AGE",
		"vm-servicescrapes": "NAME,STATUS,JOB LABEL,REASON,AGE",
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
		if strings.Join(got, ",") != cols {
			t.Errorf("%s headers = %q, want %q", k.Key, strings.Join(got, ","), cols)
		}
		// All four CRDs are scope: Namespaced upstream. namespaced:false would
		// stop applyNamespace filtering and leak every namespace into the view.
		if !k.Namespaced {
			t.Errorf("%s is declared cluster-scoped, but the upstream CRD is Namespaced", k.Key)
		}
	}
}

// The discovery gate is group/version AND resource, for EVERY kind. A kind
// living outside `requires` would put the pack on clusters without the
// operator; a wrong plural drops the whole pack on clusters that have it.
func TestVMPackGatesOnEveryKind(t *testing.T) {
	p := vmPack(t)

	if len(p.Requires) != 1 || p.Requires[0] != "operator.victoriametrics.com/v1beta1" {
		t.Fatalf("requires = %v, want exactly [operator.victoriametrics.com/v1beta1]", p.Requires)
	}
	wantRes := map[string]bool{
		"vm-agents":         false,
		"vm-alerts":         false,
		"vm-rules":          false,
		"vm-servicescrapes": false,
	}
	plural := map[string]string{
		"vm-agents":         "vmagents",
		"vm-alerts":         "vmalerts",
		"vm-rules":          "vmrules",
		"vm-servicescrapes": "vmservicescrapes",
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
		wantRes[k.Key] = true
	}
	for key, seen := range wantRes {
		if !seen {
			t.Errorf("kind %q missing from the pack", key)
		}
	}
}

// keys and shorts share ONE flat namespace, and a single collision drops the
// whole pack silently at registry build time.
func TestVMPackNamesAreFree(t *testing.T) {
	p := vmPack(t)

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
				t.Errorf("%q is already claimed by %s — the victoriametrics pack would be dropped entirely", n, owner)
			}
			taken[n] = "pack " + p.Name
		}
	}
}

// The five UpdateStatus constants are the complete set the operator can write
// (expanding, operational, failed, paused, ignored) and every one is reachable
// at runtime. Grading them wrong sorts a failed VMAgent below a healthy one.
func TestVMPackUpdateStatusSeverity(t *testing.T) {
	p := vmPack(t)

	sev, ok := p.Severities["vm-update"]
	if !ok {
		t.Fatal("severity table \"vm-update\" missing")
	}
	for value, want := range map[string]Level{
		"operational": LevelOK,
		"expanding":   LevelWarn,
		"paused":      LevelWarn,
		"ignored":     LevelWarn,
		"failed":      LevelError,
		"":            LevelUnknown, // never graded anyway: lensRows skips empty cells
	} {
		if got := sev.Level(value); got != want {
			t.Errorf("updateStatus %q graded %v, want %v", value, got, want)
		}
	}

	// Every STATUS column must actually reference the table; an ungraded
	// status column is a plain string and the table stops sorting worst-first.
	for _, k := range p.Kinds {
		for _, c := range k.Columns {
			if c.Header != "STATUS" {
				continue
			}
			if c.Path != ".status.updateStatus" {
				t.Errorf("%s STATUS path = %q, want .status.updateStatus (.status.status is the pre-v0.51 name)", k.Key, c.Path)
			}
			if c.Severity != "vm-update" {
				t.Errorf("%s STATUS severity = %q, want vm-update", k.Key, c.Severity)
			}
		}
	}
}

// Paused lives in CommonAppsParams, which only VMAgentSpec and VMAlertSpec
// embed. Offering it on VMRule/VMServiceScrape would patch a field the CRD
// schema prunes, and show a column empty on every row.
func TestVMPauseOnlyWhereSpecPausedExists(t *testing.T) {
	p := vmPack(t)

	pausable := map[string]bool{"vm-agents": true, "vm-alerts": true}
	for _, k := range p.Kinds {
		var hasAction bool
		for _, a := range k.Actions {
			if a == "vm-pause" || a == "vm-resume" {
				hasAction = true
			}
		}
		if hasAction != pausable[k.Key] {
			t.Errorf("%s pause/resume = %v, want %v", k.Key, hasAction, pausable[k.Key])
		}
		for _, c := range k.Columns {
			if c.Header == "PAUSED" && !pausable[k.Key] {
				t.Errorf("%s declares a PAUSED column, but its spec has no paused field", k.Key)
			}
		}
	}
}
