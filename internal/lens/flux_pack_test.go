package lens

import (
	"strings"
	"testing"
)

// fluxPack is the SHIPPED pack, not a literal: these tests exist to prove that
// the file in builtin/ parses and carries the columns we think it carries.
func fluxPack(t *testing.T) Pack {
	t.Helper()
	packs, errs := Builtins()
	for _, err := range errs {
		t.Fatalf("builtin packs failed to parse: %v", err)
	}
	for _, p := range packs {
		if p.Name == "flux" {
			return p
		}
	}
	t.Fatal("builtin pack \"flux\" not found")
	return Pack{}
}

// Exact headers, not a contains check: a dropped column renders empty on every
// row forever and reads as a k10s bug rather than a field that does not exist.
func TestFluxPackColumns(t *testing.T) {
	p := fluxPack(t)

	want := map[string]string{
		"flux-kustomizations": "NAME,READY,SUSPENDED,REVISION,MESSAGE,AGE",
		"flux-gitrepos":       "NAME,READY,SUSPENDED,URL,REVISION,MESSAGE,AGE",
		"flux-helmrepos":      "NAME,READY,SUSPENDED,TYPE,URL,MESSAGE,AGE",
		"flux-helmreleases":   "NAME,READY,SUSPENDED,CHART,VERSION,MESSAGE,AGE",
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
		// All five Flux CRDs are scope: Namespaced. namespaced:false would stop
		// applyNamespace filtering and leak every namespace into the view.
		if !k.Namespaced {
			t.Errorf("%s is declared cluster-scoped, but the upstream CRD is Namespaced", k.Key)
		}
	}
}

// The discovery gate is group/version AND resource, for EVERY kind, and it is
// all-or-nothing: one wrong plural or one kind outside `requires` hides the
// whole pack on clusters that do run Flux.
func TestFluxPackGatesOnEveryKind(t *testing.T) {
	p := fluxPack(t)

	wantReq := map[string]bool{
		"kustomize.toolkit.fluxcd.io/v1": true,
		"source.toolkit.fluxcd.io/v1":    true,
		"helm.toolkit.fluxcd.io/v2":      true,
	}
	if len(p.Requires) != len(wantReq) {
		t.Fatalf("requires = %v, want the three Flux group/versions", p.Requires)
	}
	for _, r := range p.Requires {
		if !wantReq[r] {
			t.Errorf("unexpected requires entry %q", r)
		}
	}
	plural := map[string]string{
		"flux-kustomizations": "kustomizations",
		"flux-gitrepos":       "gitrepositories",
		"flux-ocirepos":       "ocirepositories",
		"flux-helmrepos":      "helmrepositories",
		"flux-helmreleases":   "helmreleases",
	}
	for _, k := range p.Kinds {
		g, v, r, err := ParseGVR(k.GVR)
		if err != nil {
			t.Fatalf("%s: bad gvr %q: %v", k.Key, k.GVR, err)
		}
		if !wantReq[g+"/"+v] {
			t.Errorf("%s lives at %s/%s, outside requires %v", k.Key, g, v, p.Requires)
		}
		if r != plural[k.Key] {
			t.Errorf("%s resource = %q, want %q", k.Key, r, plural[k.Key])
		}
	}
}

// keys and shorts share ONE flat namespace; a single collision drops the whole
// pack silently at registry build time.
func TestFluxPackNamesAreFree(t *testing.T) {
	p := fluxPack(t)

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
				t.Errorf("%q is already claimed by %s — the flux pack would be dropped entirely", n, owner)
			}
			taken[n] = "pack " + p.Name
		}
	}
}

// Ready is a metav1.Condition, so its status is exactly True/False/Unknown.
// Grading it wrong sorts a failed Kustomization below a healthy one.
func TestFluxPackReadySeverity(t *testing.T) {
	p := fluxPack(t)

	sev, ok := p.Severities["flux-ready"]
	if !ok {
		t.Fatal("severity table \"flux-ready\" missing")
	}
	for value, want := range map[string]Level{
		"True":    LevelOK,
		"False":   LevelError,
		"Unknown": LevelWarn, // reconciling / not yet observed, not a doubt
		"":        LevelUnknown,
	} {
		if got := sev.Level(value); got != want {
			t.Errorf("Ready %q graded %v, want %v", value, got, want)
		}
	}

	// Every READY column must actually reference the table; an ungraded status
	// column is a plain string and the table stops sorting worst-first.
	for _, k := range p.Kinds {
		for _, c := range k.Columns {
			if c.Header == "READY" && c.Severity != "flux-ready" {
				t.Errorf("%s READY severity = %q, want flux-ready", k.Key, c.Severity)
			}
		}
	}
}

// reconcile/suspend/resume are the reason this pack exists and every kind
// supports them; force/reset are HelmRelease-only (Kustomization and the source
// kinds have no lastHandledForceAt and their controllers never read forceAt).
func TestFluxPackActionsPerKind(t *testing.T) {
	p := fluxPack(t)

	for _, k := range p.Kinds {
		has := map[string]bool{}
		for _, a := range k.Actions {
			has[a] = true
		}
		for _, want := range []string{"flux-suspend", "flux-resume"} {
			if !has[want] {
				t.Errorf("%s is missing action %q", k.Key, want)
			}
		}
		// Reconcile is on every kind EXCEPT HelmRepositories: since Flux v2.2
		// an OCI HelmRepository is never reconciled at all, so the annotation
		// would be written, acked against a .status.lastHandledReconcileAt no
		// controller will ever set, and spin for the full 45s timeout.
		if wantReconcile := k.Key != "flux-helmrepos"; has["flux-reconcile"] != wantReconcile {
			t.Errorf("%s flux-reconcile = %v, want %v", k.Key, has["flux-reconcile"], wantReconcile)
		}
		helm := k.Key == "flux-helmreleases"
		for _, only := range []string{"flux-force", "flux-reset"} {
			if has[only] != helm {
				t.Errorf("%s declares %q = %v, want %v", k.Key, only, has[only], helm)
			}
		}
	}

	// A single-key annotate is what makes ackWant return a real token; a
	// two-key annotate falls back to a presence check that is already true
	// after the first ever force, so force/reset must ship with no ack.
	for _, a := range p.Actions {
		switch a.ID {
		case "flux-reconcile":
			if len(a.Annotations) != 1 || a.Annotations["reconcile.fluxcd.io/requestedAt"] != "{{.Now}}" {
				t.Errorf("reconcile annotations = %v, want the single requestedAt token", a.Annotations)
			}
			if a.Ack != ".status.lastHandledReconcileAt" {
				t.Errorf("reconcile ack = %q, want .status.lastHandledReconcileAt", a.Ack)
			}
		case "flux-force", "flux-reset":
			if len(a.Annotations) != 2 {
				t.Errorf("%s writes %d annotations, want 2 (requestedAt must carry the same token)", a.ID, len(a.Annotations))
			}
			if a.Annotations["reconcile.fluxcd.io/requestedAt"] != "{{.Now}}" {
				t.Errorf("%s does not write requestedAt; HandleAnnotationRequest ignores it unless both keys match", a.ID)
			}
			if a.Ack != "" {
				t.Errorf("%s declares ack %q, but a two-key annotate can only be presence-checked and would lie", a.ID, a.Ack)
			}
		}
	}
}
