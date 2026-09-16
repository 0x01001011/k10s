package lens

import "testing"

// cnpgPack is the SHIPPED pack, not a literal: these tests exist to prove the
// file in builtin/ grades the values CloudNativePG actually publishes.
func cnpgPack(t *testing.T) Pack {
	t.Helper()
	packs, errs := Builtins()
	for _, err := range errs {
		t.Fatalf("builtin packs failed to parse: %v", err)
	}
	for _, p := range packs {
		if p.Name == "cnpg" {
			return p
		}
	}
	t.Fatal(`builtin pack "cnpg" not found`)
	return Pack{}
}

// A severity table that names a value the operator never writes is worse than
// no table at all: it grades every row warn while looking deliberate.
//
// Pooler.status.phase is an enum — active, paused, inactive, failed — not the
// sentence Cluster.status.phase uses. The pack shipped with ok: ["Pooler is
// ready"], which matches nothing, so a perfectly healthy PgBouncer has always
// rendered amber.
//
// https://cloudnative-pg.io/docs/1.30/cloudnative-pg.v1/#postgresql-cnpg-io-v1-PoolerStatus
func TestCnpgPoolerSeverityGradesTheRealEnum(t *testing.T) {
	sev, ok := cnpgPack(t).Severities["pooler-phase"]
	if !ok {
		t.Fatal("pack declares no pooler-phase severity table")
	}

	for _, c := range []struct {
		value string
		want  Level
	}{
		{"active", LevelOK},
		{"failed", LevelError},
		{"paused", LevelWarn},
		{"inactive", LevelWarn},
		// An enum CNPG adds tomorrow must not silently read as healthy.
		{"somethingNew", LevelWarn},
	} {
		if got := sev.Level(c.value); got != c.want {
			t.Errorf("pooler phase %q grades %v, want %v", c.value, got, c.want)
		}
	}
}

// The cluster table is the one place the sentence form is correct: CNPG writes
// a human sentence into Cluster.status.phase, and exactly one of them is good.
func TestCnpgClusterSeverityGradesOnlyTheHealthySentence(t *testing.T) {
	sev, ok := cnpgPack(t).Severities["cluster-phase"]
	if !ok {
		t.Fatal("pack declares no cluster-phase severity table")
	}

	if got := sev.Level("Cluster in healthy state"); got != LevelOK {
		t.Errorf("healthy phase grades %v, want ok", got)
	}
	for _, bad := range []string{
		"Failing over",
		"Switchover in progress",
		"Cluster is unrecoverable and needs manual intervention",
		"Upgrading Postgres major version",
	} {
		if got := sev.Level(bad); got == LevelOK {
			t.Errorf("phase %q grades ok, which it is not", bad)
		}
	}
}
