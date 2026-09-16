package lens

import (
	"strings"
	"testing"
)

// certManagerPack is the SHIPPED pack, not a literal: these tests exist to
// prove that the file in builtin/ parses and carries the columns we think it
// carries.
func certManagerPack(t *testing.T) Pack {
	t.Helper()
	packs, errs := Builtins()
	for _, err := range errs {
		t.Fatalf("builtin packs failed to parse: %v", err)
	}
	for _, p := range packs {
		if p.Name == "cert-manager" {
			return p
		}
	}
	t.Fatal("builtin pack \"cert-manager\" not found")
	return Pack{}
}

// Exact headers, not a contains check: a dropped column renders empty on every
// row forever and reads as a k10s bug rather than as a field that does not
// exist upstream.
func TestCertManagerPackColumns(t *testing.T) {
	p := certManagerPack(t)

	want := map[string]string{
		"certmgr-certificates":   "NAME,READY,EXPIRES,RENEWAL,SECRET,ISSUER,MESSAGE,AGE",
		"certmgr-requests":       "NAME,READY,REASON,ISSUER,REQUESTER,FAILED,AGE",
		"certmgr-issuers":        "NAME,READY,MESSAGE,AGE",
		"certmgr-clusterissuers": "NAME,READY,MESSAGE,AGE",
	}
	// clusterissuers is scope: Cluster upstream; the other three are
	// Namespaced. Declaring the scope wrong either leaks every namespace into
	// the view or lists nothing at all.
	scope := map[string]bool{
		"certmgr-certificates":   true,
		"certmgr-requests":       true,
		"certmgr-issuers":        true,
		"certmgr-clusterissuers": false,
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
		if k.Namespaced != scope[k.Key] {
			t.Errorf("%s namespaced = %v, want %v", k.Key, k.Namespaced, scope[k.Key])
		}
	}
}

// EXPIRES is the headline column, and .status.notAfter is a FUTURE instant:
// format: age would render "<invalid>" on every healthy certificate.
func TestCertManagerExpiryUsesUntil(t *testing.T) {
	p := certManagerPack(t)

	wantFormat := map[string]string{"EXPIRES": "until", "RENEWAL": "until", "AGE": "age"}
	wantPath := map[string]string{
		"EXPIRES": ".status.notAfter",
		"RENEWAL": ".status.renewalTime",
	}
	var seen int
	for _, k := range p.Kinds {
		if k.Key != "certmgr-certificates" {
			continue
		}
		for _, c := range k.Columns {
			f, ok := wantFormat[c.Header]
			if !ok {
				continue
			}
			if c.Format != f {
				t.Errorf("%s format = %q, want %q", c.Header, c.Format, f)
			}
			if pth, ok := wantPath[c.Header]; ok {
				if c.Path != pth {
					t.Errorf("%s path = %q, want %q", c.Header, c.Path, pth)
				}
				seen++
			}
		}
	}
	if seen != len(wantPath) {
		t.Errorf("certmgr-certificates carries %d of the %d time columns", seen, len(wantPath))
	}
}

// The discovery gate is group/version AND resource, for EVERY kind. A wrong
// plural hides the whole pack on clusters that do run cert-manager.
func TestCertManagerPackGatesOnEveryKind(t *testing.T) {
	p := certManagerPack(t)

	if len(p.Requires) != 1 || p.Requires[0] != "cert-manager.io/v1" {
		t.Fatalf("requires = %v, want exactly [cert-manager.io/v1]", p.Requires)
	}
	plural := map[string]string{
		"certmgr-certificates":   "certificates",
		"certmgr-requests":       "certificaterequests",
		"certmgr-issuers":        "issuers",
		"certmgr-clusterissuers": "clusterissuers",
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

// keys and shorts share ONE flat namespace; a single collision drops the whole
// pack silently at registry build time.
func TestCertManagerPackNamesAreFree(t *testing.T) {
	p := certManagerPack(t)

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
				t.Errorf("%q is already claimed by %s — the cert-manager pack would be dropped entirely", n, owner)
			}
			taken[n] = "pack " + p.Name
		}
	}
}

// A Certificate whose Ready condition is False is broken and belongs at the
// top of the table. A CertificateRequest that is not yet Ready is merely
// pending — grading that False as an error would float every in-flight
// request above genuinely failed certificates.
func TestCertManagerReadySeverity(t *testing.T) {
	p := certManagerPack(t)

	for table, wantFalse := range map[string]Level{
		"cert-ready": LevelError,
		"req-ready":  LevelWarn,
	} {
		sev, ok := p.Severities[table]
		if !ok {
			t.Fatalf("severity table %q missing", table)
		}
		for value, want := range map[string]Level{
			"True": LevelOK, "False": wantFalse, "Unknown": LevelUnknown, "": LevelUnknown,
		} {
			if got := sev.Level(value); got != want {
				t.Errorf("%s: %q graded %v, want %v", table, value, got, want)
			}
		}
	}

	// Every READY column must actually reference a table, or the kind stops
	// sorting worst-first entirely (lensHasSeverity).
	wantTable := map[string]string{
		"certmgr-certificates":   "cert-ready",
		"certmgr-requests":       "req-ready",
		"certmgr-issuers":        "cert-ready",
		"certmgr-clusterissuers": "cert-ready",
	}
	for _, k := range p.Kinds {
		for _, c := range k.Columns {
			if c.Header != "READY" {
				continue
			}
			if c.Severity != wantTable[k.Key] {
				t.Errorf("%s READY severity = %q, want %q", k.Key, c.Severity, wantTable[k.Key])
			}
		}
	}
}

// Only the Secret edge is verified at source. A CertificateRequest ->
// Certificate parent edge rests on an ownerReferences convention nobody
// declares in the API types, so it is deliberately absent.
func TestCertManagerEdges(t *testing.T) {
	p := certManagerPack(t)

	if len(p.Edges) != 1 {
		t.Fatalf("pack declares %d edges, want exactly 1", len(p.Edges))
	}
	e := p.Edges[0]
	if e.From != "cert-manager.io/v1/certificates" || e.To != "v1/secrets" {
		t.Errorf("edge = %s -> %s, want cert-manager.io/v1/certificates -> v1/secrets", e.From, e.To)
	}
	if e.Via != ViaField || e.Key != ".spec.secretName" {
		t.Errorf("edge via/key = %s/%s, want field/.spec.secretName", e.Via, e.Key)
	}
}
