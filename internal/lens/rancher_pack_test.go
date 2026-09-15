package lens

import (
	"strings"
	"testing"
)

// rancherPack is the SHIPPED pack, not a literal: the point of these tests is
// that the file in builtin/ parses and carries the columns we think it does.
func rancherPack(t *testing.T) Pack {
	t.Helper()
	packs, errs := Builtins()
	for _, err := range errs {
		t.Fatalf("builtin packs failed to parse: %v", err)
	}
	for _, p := range packs {
		if p.Name == "rancher" {
			return p
		}
	}
	t.Fatal("builtin pack \"rancher\" not found")
	return Pack{}
}

// Exact headers, not a contains check: a dropped column is precisely the bug
// this is here to catch, since an empty cell reads as a k10s bug.
func TestRancherPackColumns(t *testing.T) {
	p := rancherPack(t)

	want := map[string]string{
		"rancher-clusters": "NAME,DISPLAY,READY,PROVISIONED,PROVIDER,VERSION,NODES,AGE",
		"rancher-repos":    "NAME,URL,GITREPO,BRANCH,DOWNLOADED,COMMIT,LAST-DOWNLOAD,AGE",
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
		// Both Rancher kinds are cluster-scoped upstream (+genclient:nonNamespaced);
		// a namespaced:true here would make every row vanish behind applyNamespace.
		if k.Namespaced {
			t.Errorf("%s is namespaced, but the upstream type is cluster-scoped", k.Key)
		}
	}
}

// The gate is group/version AND resource, all kinds. Both Rancher kinds must be
// covered or the pack shows up on a cluster without Rancher.
func TestRancherPackGatesOnEveryKind(t *testing.T) {
	p := rancherPack(t)

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
}

// keys and shorts share one flat namespace, and a single collision drops the
// WHOLE pack — silently, at registry build time.
func TestRancherPackNamesAreFree(t *testing.T) {
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
