package lens

import (
	"bytes"
	"strings"
	"testing"

	"k8s.io/client-go/util/jsonpath"
)

// fleetPack is the SHIPPED pack, not a literal: the point of these tests is
// that the file in builtin/ parses and carries the columns we think it does.
func fleetPack(t *testing.T) Pack {
	t.Helper()
	packs, errs := Builtins()
	for _, err := range errs {
		t.Fatalf("builtin packs failed to parse: %v", err)
	}
	for _, p := range packs {
		if p.Name == "fleet" {
			return p
		}
	}
	t.Fatal("builtin pack \"fleet\" not found")
	return Pack{}
}

// Exact headers, not a contains check: a dropped column is precisely the bug
// this is here to catch, since an empty cell reads as a k10s bug rather than
// as a field that does not exist.
func TestFleetPackColumns(t *testing.T) {
	p := fleetPack(t)

	want := map[string]string{
		// No REPO on either kind, and no MESSAGE on Bundles: at the reference
		// width those cells render the same constant prefix on every row
		// (`https:/…`, `fleet-def…`) and take the width from the columns that
		// do discriminate. See the pack's own comments.
		"fleet-gitrepos":          "NAME,BRANCH,COMMIT,READY-BD,STATE,PAUSED,MESSAGE,AGE",
		"fleet-bundles":           "NAME,READY,STATE,WORST-CLUSTER,PAUSED,AGE",
		"fleet-bundledeployments": "NAME,BUNDLE,BUNDLE-NS,READY,DEPLOYED,MONITORED,STATE,AGE",
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
		// Every Fleet CRD here is Namespaced upstream; a namespaced:false
		// would make applyNamespace stop filtering and show every namespace.
		if !k.Namespaced {
			t.Errorf("%s is cluster-scoped, but the upstream CRD is Namespaced", k.Key)
		}
	}
}

// The gate is group/version AND resource, for EVERY kind. One kind outside
// `requires` and the pack shows up on a cluster without Fleet.
func TestFleetPackGatesOnEveryKind(t *testing.T) {
	p := fleetPack(t)

	req := map[string]bool{}
	for _, r := range p.Requires {
		req[r] = true
	}
	for _, k := range p.Kinds {
		g, v, _, err := ParseGVR(k.GVR)
		if err != nil {
			t.Fatalf("%s: bad gvr %q: %v", k.Key, k.GVR, err)
		}
		if !req[g+"/"+v] {
			t.Errorf("%s lives at %s/%s, which is not in requires %v", k.Key, g, v, p.Requires)
		}
	}
	if len(p.Requires) == 0 {
		t.Fatal("pack is not discovery-gated")
	}
}

// keys and shorts share one flat namespace, and a single collision drops the
// WHOLE pack — silently, at registry build time.
func TestFleetPackNamesAreFree(t *testing.T) {
	taken := map[string]string{}
	builtinK8s := strings.Fields(`
		pods deployments replicasets statefulsets daemonsets jobs cronjobs hpas services
		endpoints ingresses networkpolicies configmaps secrets resourcequotas limitranges
		pdbs pvcs pvs storageclasses serviceaccounts roles rolebindings clusterroles
		clusterrolebindings nodes namespaces events crds customresources
		po deploy rs sts ds job cj hpa svc ep ing netpol cm sec quota limits pdb pvc pv
		sc sa role rb crole crb no ns ev crd cr`)
	for _, n := range builtinK8s {
		taken[n] = "k8s builtin"
	}

	packs, _ := Builtins()
	for _, p := range packs {
		for _, k := range p.Kinds {
			for _, n := range []string{k.Key, k.Short} {
				if n == "" {
					continue
				}
				if owner, dup := taken[n]; dup {
					t.Errorf("%q claimed by both %s and pack %q — the later pack is dropped entirely", n, owner, p.Name)
				}
				taken[n] = "pack " + p.Name
			}
		}
	}
}

// The label columns and the label edges must agree with the escaping k8s
// jsonpath actually needs: '.metadata.labels.fleet.cattle.io/repo-name'
// without the backslashes renders EMPTY on every row, silently, because the
// parser splits the key on its dots. Nothing else catches that.
func TestFleetLabelColumnsResolve(t *testing.T) {
	p := fleetPack(t)

	obj := map[string]any{
		"metadata": map[string]any{
			"labels": map[string]any{
				"fleet.cattle.io/repo-name":        "my-repo",
				"fleet.cattle.io/bundle-name":      "my-bundle",
				"fleet.cattle.io/bundle-namespace": "fleet-default",
			},
		},
	}
	// The escaped-dot label form is what this guards; fleet-bundles/REPO used
	// to be the third case and was dropped as a column, so the two
	// BundleDeployment labels carry it.
	want := map[string]string{
		"fleet-bundledeployments/BUNDLE":    "my-bundle",
		"fleet-bundledeployments/BUNDLE-NS": "fleet-default",
	}
	for _, k := range p.Kinds {
		for _, c := range k.Columns {
			exp, ok := want[k.Key+"/"+c.Header]
			if !ok {
				continue
			}
			jp := jsonpath.New(c.Header)
			jp.AllowMissingKeys(true)
			if err := jp.Parse(Braced(c.Path)); err != nil {
				t.Fatalf("%s %s: %v", k.Key, c.Header, err)
			}
			var buf bytes.Buffer
			if err := jp.Execute(&buf, obj); err != nil {
				t.Fatalf("%s %s: %v", k.Key, c.Header, err)
			}
			if buf.String() != exp {
				t.Errorf("%s %s = %q, want %q (path %q)", k.Key, c.Header, buf.String(), exp, c.Path)
			}
			delete(want, k.Key+"/"+c.Header)
		}
	}
	for miss := range want {
		t.Errorf("column %s never appeared in the pack", miss)
	}
}
