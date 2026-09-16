package lens

import (
	"strings"
	"testing"
)

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

func cnpgAction(t *testing.T, id string) Action {
	t.Helper()
	a, ok := cnpgPack(t).Action(id)
	if !ok {
		t.Fatalf("pack has no action %q", id)
	}
	return a
}

func cnpgKind(t *testing.T, key string) Kind {
	t.Helper()
	for _, k := range cnpgPack(t).Kinds {
		if k.Key == key {
			return k
		}
	}
	t.Fatalf("pack has no kind %q", key)
	return Kind{}
}

func paramOf(t *testing.T, a Action, name string) Param {
	t.Helper()
	for _, p := range a.Params {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("action %q has no param %q", a.ID, name)
	return Param{}
}

func headers(k Kind) string {
	var out []string
	for _, c := range k.Columns {
		out = append(out, c.Header)
	}
	return strings.Join(out, ",")
}

// Fencing and promoting both act on an INSTANCE, and CNPG names instances
// "<cluster>-N". The instance list comes from the cluster's own status rather
// than from the operator's memory.
func TestCnpgInstanceActionsOfferTheClustersInstances(t *testing.T) {
	for _, id := range []string{"cnpg-fence", "cnpg-promote"} {
		a := cnpgAction(t, id)
		p := paramOf(t, a, "instance")
		if p.OptionsFrom != ".status.instanceNames" {
			t.Errorf("%s: instance optionsFrom = %q, want .status.instanceNames", id, p.OptionsFrom)
		}
		if !p.Required {
			t.Errorf("%s: instance must be required — an empty one targets nothing", id)
		}
		if a.RequiresSelection {
			t.Errorf("%s still uses requiresSelection; params replaced it", id)
		}
		// The word typed to confirm has to be the instance, not the cluster:
		// typing the cluster's name confirms something never shown.
		if a.ConfirmValue != "{{.Params.instance}}" {
			t.Errorf("%s: confirmValue = %q", id, a.ConfirmValue)
		}
	}
}

// The guards kubectl-cnpg applies before patching. Promoting the instance that
// is already the target is a no-op dressed as an action; promoting a fenced
// instance cannot succeed, because a fenced instance has no running Postgres.
func TestCnpgPromoteCarriesItsRefusals(t *testing.T) {
	a := cnpgAction(t, "cnpg-promote")
	if len(a.RefuseWhen) == 0 {
		t.Fatal("promote declares no preconditions")
	}
	var paths []string
	for _, r := range a.RefuseWhen {
		paths = append(paths, r.Path)
		if strings.TrimSpace(r.Reason) == "" {
			t.Errorf("refusal on %q has no reason", r.Path)
		}
	}
	joined := strings.Join(paths, " ")
	if !strings.Contains(joined, "fencedInstances") {
		t.Errorf("promote does not refuse on a fenced cluster: %v", paths)
	}
}

// Fencing is the action that takes a database down. Both it and promote are
// switchovers in all but name, so both stay behind the typed gate.
func TestCnpgHighStakesActionsStayTyped(t *testing.T) {
	for _, id := range []string{"cnpg-fence", "cnpg-promote", "cnpg-hibernate", "cnpg-restore"} {
		if got := cnpgAction(t, id).Confirm; got != ConfirmTyped {
			t.Errorf("%s confirm = %q, want typed", id, got)
		}
	}
}

// The columns an SRE actually reads. ContinuousArchiving is the one that
// matters most: when it is False, WAL archiving is broken and every backup
// taken since is a lie.
func TestCnpgClusterColumnsCarryTheOperationalSignals(t *testing.T) {
	k := cnpgKind(t, "cnpg-clusters")
	got := headers(k)

	for _, want := range []string{"WAL", "NODES"} {
		if !strings.Contains(got, want) {
			t.Errorf("cluster headers = %q, missing %s", got, want)
		}
	}
	var wal Column
	for _, c := range k.Columns {
		if c.Header == "WAL" {
			wal = c
		}
	}
	if !strings.Contains(wal.Path, "ContinuousArchiving") {
		t.Errorf("WAL column reads %q, want the ContinuousArchiving condition", wal.Path)
	}
	if wal.Severity == "" {
		t.Error("WAL column is ungraded, so a broken archive renders the same as a working one")
	}
	sev := cnpgPack(t).Severities[wal.Severity]
	if sev.Level("False") != LevelError {
		t.Errorf("ContinuousArchiving=False grades %v, want error", sev.Level("False"))
	}
	if sev.Level("True") != LevelOK {
		t.Errorf("ContinuousArchiving=True grades %v, want ok", sev.Level("True"))
	}
}

// Backup.spec is immutable after creation, so a wrong method cannot be edited
// — only deleted and recreated. The form has to offer the real values.
func TestCnpgBackupParametersMatchTheAPI(t *testing.T) {
	a := cnpgAction(t, "cnpg-backup")

	method := paramOf(t, a, "method")
	var vals []string
	for _, o := range method.Options {
		vals = append(vals, o.Value)
	}
	if got := strings.Join(vals, ","); got != "barmanObjectStore,volumeSnapshot,plugin" {
		t.Errorf("method options = %q, want the three BackupMethod values", got)
	}

	target := paramOf(t, a, "target")
	vals = nil
	for _, o := range target.Options {
		vals = append(vals, o.Value)
	}
	if got := strings.Join(vals, ","); got != "primary,prefer-standby" {
		t.Errorf("target options = %q, want the two BackupTarget values", got)
	}
}

// The declarative CRDs CNPG shipped in 1.25 and after. Each has the same tiny
// status — applied, message — which is exactly what the table should show.
func TestCnpgDeclarativeKindsExist(t *testing.T) {
	want := map[string]bool{
		"cnpg-databases":     true,
		"cnpg-publications":  true,
		"cnpg-subscriptions": true,
		"cnpg-imagecatalogs": true,
	}
	for key := range want {
		k := cnpgKind(t, key)
		if !strings.Contains(headers(k), "APPLIED") {
			t.Errorf("%s headers = %q, missing APPLIED", key, headers(k))
		}
	}

	// applied:false is a failure the operator has to act on, not a neutral
	// state — the object exists and the database change did not happen.
	sev, ok := cnpgPack(t).Severities["applied"]
	if !ok {
		t.Fatal("pack declares no applied severity table")
	}
	if sev.Level("false") != LevelError {
		t.Errorf("applied=false grades %v, want error", sev.Level("false"))
	}
	if sev.Level("true") != LevelOK {
		t.Errorf("applied=true grades %v, want ok", sev.Level("true"))
	}
}

// CNPG never restores in place: recovery is always a NEW Cluster bootstrapped
// from a backup. The action has to create one, and say so.
func TestCnpgRestoreCreatesANewCluster(t *testing.T) {
	a := cnpgAction(t, "cnpg-restore")

	if a.Verb != VerbCreate {
		t.Errorf("restore verb = %q, want create", a.Verb)
	}
	if a.Template["kind"] != "Cluster" {
		t.Errorf("restore creates %v, want a Cluster", a.Template["kind"])
	}
	// The notice is the only place this can be said. An operator reaching for
	// "restore" on a broken cluster expects that cluster to be repaired.
	if !strings.Contains(strings.ToLower(a.Notice), "new cluster") {
		t.Errorf("restore notice does not say it creates a new cluster: %q", a.Notice)
	}

	spec, _ := a.Template["spec"].(map[string]any)
	boot, _ := spec["bootstrap"].(map[string]any)
	if _, ok := boot["recovery"]; !ok {
		t.Fatalf("restore template has no bootstrap.recovery: %+v", spec)
	}

	// The source backup is chosen from the cluster's own Backups, through the
	// edge the pack already declares.
	src := paramOf(t, a, "source")
	if src.OptionsFrom != "postgresql.cnpg.io/v1/backups" {
		t.Errorf("source optionsFrom = %q, want the Backup GVR", src.OptionsFrom)
	}
	if !src.Required {
		t.Error("source must be required — a recovery with no backup cannot bootstrap")
	}

	// A recovery target is free text: a timestamp or an LSN cannot be
	// enumerated from the cluster.
	if tgt := paramOf(t, a, "targetTime"); !tgt.AllowFree {
		t.Error("targetTime must allow a free value")
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
