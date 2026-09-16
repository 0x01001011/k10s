package lens

import (
	"strings"
	"testing"
)

// shippedPack is the pack as it is BUILT INTO the binary, not a literal: these
// tests exist to prove the files in builtin/ ask for what they need.
func shippedPack(t *testing.T, name string) Pack {
	t.Helper()
	packs, errs := Builtins()
	for _, err := range errs {
		t.Fatalf("builtin packs failed to parse: %v", err)
	}
	for _, p := range packs {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("builtin pack %q not found", name)
	return Pack{}
}

func shippedAction(t *testing.T, pack, id string) Action {
	t.Helper()
	a, ok := shippedPack(t, pack).Action(id)
	if !ok {
		t.Fatalf("pack %q has no action %q", pack, id)
	}
	return a
}

// actionMentions reports whether any template string in the action contains
// needle — including map KEYS, which is not a detail: Kargo's approvedFor is
// keyed by the Stage name, so the one value that action collects appears
// nowhere else.
func actionMentions(a Action, needle string) bool {
	found := false
	var tree func(any)
	tree = func(n any) {
		switch v := n.(type) {
		case string:
			if strings.Contains(v, needle) {
				found = true
			}
		case map[string]any:
			for k, e := range v {
				tree(k)
				tree(e)
			}
		case []any:
			for _, e := range v {
				tree(e)
			}
		}
	}
	for k, v := range a.Annotations {
		tree(k)
		tree(v)
	}
	tree(a.Patch)
	tree(a.Template)
	tree(a.ConfirmValue)
	return found
}

// The retirement itself. .Selected was one slot, filled by a free-text prompt
// with no suggestions and no preview, and it defaulted to the row's own name —
// so an action that forgot to declare requiresSelection wrote the cluster's
// name where an instance belonged and looked like it worked. Parameters
// replace it: named, validated, and sourced from the cluster.
func TestNoShippedActionTemplatesSelected(t *testing.T) {
	packs, _ := Builtins()
	for _, p := range packs {
		for _, a := range p.Actions {
			if actionMentions(a, ".Selected") {
				t.Errorf("lens %q action %q still templates {{.Selected}}; declare a param instead", p.Name, a.ID)
			}
		}
	}
}

// Every value an action collects has to reach a template, or the form is
// asking a question nobody reads.
func TestEveryDeclaredParamIsUsed(t *testing.T) {
	packs, _ := Builtins()
	for _, p := range packs {
		for _, a := range p.Actions {
			for _, param := range a.Params {
				if !actionMentions(a, ".Params."+param.Name) {
					t.Errorf("lens %q action %q declares param %q but no template uses it",
						p.Name, a.ID, param.Name)
				}
			}
		}
	}
}

// A git revision is the value nobody can type from memory, which makes it the
// one that most needed a list. .status.history is what `argocd app history`
// prints, so the rollback form offers the same thing.
func TestArgoRollbackOffersTheDeployedRevisions(t *testing.T) {
	a := shippedAction(t, "argocd", "argocd-rollback")
	p := paramOf(t, a, "revision")

	if p.OptionsFrom != ".status.history[*].revision" {
		t.Errorf("revision optionsFrom = %q, want the deployment history", p.OptionsFrom)
	}
	if !p.Required {
		t.Error("revision must be required — an empty one rolls back to nothing")
	}
	// The history is capped (revisionHistoryLimit, 10 by default), so a
	// revision older than the cap is still a legal answer.
	if !p.AllowFree {
		t.Error("revision must allow a value outside the capped history")
	}
	if a.ConfirmValue != "{{.Params.revision}}" {
		t.Errorf("confirmValue = %q, want the revision being rolled back to", a.ConfirmValue)
	}
}

// An empty id is silently ignored by Kargo's controller, which is the worst
// failure available: it looks like it worked. The ids live two arrays deep in
// the Stage's own status.
func TestKargoReverifyOffersTheVerificationIDs(t *testing.T) {
	p := paramOf(t, shippedAction(t, "kargo", "kargo-reverify"), "id")

	want := ".status.freightHistory[*].verificationHistory[*].id"
	if p.OptionsFrom != want {
		t.Errorf("id optionsFrom = %q, want %q", p.OptionsFrom, want)
	}
	if !p.Required {
		t.Error("id must be required — an empty one is accepted and does nothing")
	}
}

// Freight and Stage names cannot be enumerated from the object being acted
// on: promoting asks for Freight the Stage has never held, and approving asks
// for a Stage the Freight was never verified in. Both stay free text — but
// named, required and previewed, which the single .Selected slot was not.
func TestKargoFreeTextParamsAreStillDeclared(t *testing.T) {
	for _, c := range []struct{ id, param string }{
		{"kargo-promote", "freight"},
		{"kargo-approve", "stage"},
	} {
		p := paramOf(t, shippedAction(t, "kargo", c.id), c.param)
		if p.OptionsFrom != "" {
			t.Errorf("%s: %s claims a source (%q); neither list can be derived",
				c.id, c.param, p.OptionsFrom)
		}
		if !p.Required {
			t.Errorf("%s: %s must be required", c.id, c.param)
		}
		if !p.AllowFree {
			t.Errorf("%s: %s has no suggestions, so it must accept a free value", c.id, c.param)
		}
	}
}

// Creating a Longhorn Backup does not snapshot for you — spec.snapshotName
// must name a Snapshot that already exists. The pack already declares the
// snapshot-to-volume edge for the relationship panel, so the form reads that
// rather than a second statement of the same fact.
func TestLonghornBackupOffersTheVolumesSnapshots(t *testing.T) {
	p := paramOf(t, shippedAction(t, "longhorn", "lh-backup"), "snapshotName")

	if p.OptionsFrom != "longhorn.io/v1beta2/snapshots" {
		t.Errorf("snapshotName optionsFrom = %q, want the Snapshot GVR", p.OptionsFrom)
	}
	if !p.Required {
		t.Error("snapshotName must be required — a Backup without one is never reconciled")
	}
}
