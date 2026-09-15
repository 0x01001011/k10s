package lens

import (
	"strings"
	"testing"
)

// k3sHelmPack is the SHIPPED pack, not a literal: the point of these tests is
// that the file in builtin/ parses, not that a fixture does.
func k3sHelmPack(t *testing.T) Pack {
	t.Helper()
	packs, errs := Builtins()
	for _, err := range errs {
		t.Fatalf("builtin packs failed to parse: %v", err)
	}
	for _, p := range packs {
		if p.Name == "k3s-helm" {
			return p
		}
	}
	t.Fatal("builtin pack \"k3s-helm\" not found")
	return Pack{}
}

// Exact header lists, not a contains check: a dropped column renders as an
// empty table cell forever and reads as a k10s bug rather than a missing
// field.
func TestK3sHelmPackColumns(t *testing.T) {
	p := k3sHelmPack(t)

	want := map[string]string{
		// No REPO (a constant `https://…` prefix at the reference width) and no
		// JOB (deterministically helm-install-<name>, and `R` reaches it by
		// that exact name through the .status.jobName field edge). Both cost
		// width that VERSION and TARGET NS need to stay readable.
		"k3s-helm-charts":  "NAME,CHART,VERSION,TARGET NS,FAILED,AGE",
		"k3s-helm-configs": "NAME,FAILURE POLICY,SERVER SIDE,AGE",
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

// helm-controller's HelmChart is the only one of the two with a status
// subresource, so it is the only one that may grade a row.
func TestK3sHelmSeverityOnlyOnCharts(t *testing.T) {
	p := k3sHelmPack(t)
	for _, k := range p.Kinds {
		for _, c := range k.Columns {
			if c.Severity != "" && k.Key != "k3s-helm-charts" {
				t.Errorf("%s column %q grades a row, but HelmChartConfig has no status at all", k.Key, c.Header)
			}
		}
	}
	sev, ok := p.Severities["chart-failed"]
	if !ok {
		t.Fatal("chart-failed severity table missing")
	}
	// The condition carries corev1.ConditionStatus, so these three strings
	// are the whole domain.
	for v, want := range map[string]Level{"True": LevelError, "False": LevelOK, "Unknown": LevelUnknown} {
		if got := sev.Level(v); got != want {
			t.Errorf("Failed=%s graded %v, want %v", v, got, want)
		}
	}
}

// Every key and short shares one flat namespace with every other builtin
// pack; one collision drops this whole pack at registry build, actions and
// edges included.
func TestK3sHelmNamesDoNotCollide(t *testing.T) {
	packs, _ := Builtins()
	taken := map[string]string{}
	for _, p := range packs {
		if p.Name == "k3s-helm" {
			continue
		}
		for _, k := range p.Kinds {
			taken[k.Key] = p.Name
			if k.Short != "" {
				taken[k.Short] = p.Name
			}
		}
	}
	mine := map[string]bool{}
	for _, k := range k3sHelmPack(t).Kinds {
		for _, n := range []string{k.Key, k.Short} {
			if n == "" {
				continue
			}
			if owner, ok := taken[n]; ok {
				t.Errorf("%q is already claimed by pack %q", n, owner)
			}
			if mine[n] {
				t.Errorf("%q is claimed twice within this pack", n)
			}
			mine[n] = true
		}
	}
}

// The gate itself lives in internal/k8s; what this pack owns is the claim it
// gates on. helm.cattle.io/v1 is the only version helm-controller has ever
// served (v0.17.8: one dir under pkg/apis/helm.cattle.io).
func TestK3sHelmGatesOnItsOwnGroupVersion(t *testing.T) {
	p := k3sHelmPack(t)
	if len(p.Requires) != 1 || p.Requires[0] != "helm.cattle.io/v1" {
		t.Fatalf("requires = %v, want [helm.cattle.io/v1]", p.Requires)
	}
	// Gate 2 matches on the resource plural; a wrong plural hides the pack
	// on every cluster that does serve it.
	want := map[string]string{
		"k3s-helm-charts":  "helm.cattle.io/v1/helmcharts",
		"k3s-helm-configs": "helm.cattle.io/v1/helmchartconfigs",
	}
	for _, k := range p.Kinds {
		if k.GVR != want[k.Key] {
			t.Errorf("%s gvr = %q, want %q", k.Key, k.GVR, want[k.Key])
		}
		if !k.Namespaced {
			t.Errorf("%s must be namespaced — both kinds are", k.Key)
		}
	}
}
