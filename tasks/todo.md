# Tasks: CNPG lens — parameterised actions, honest columns, PITR restore

Plan and rationale: `tasks/plan.md`.

Definition of done for every task: `just check` passes (fmt-check → vet → test),
a test sits beside the change, and any UI change is verified against a real
frame from `just shot` rather than by reading the view code.

---

## Phase 1: Bugs

### T1: Multi-line kubectl breaks the confirm modal

**Description:** `lens.Kubectl` returns a multi-line heredoc for `VerbCreate`.
`lensConfirmBody` appends it as a single `[]string` element, `overlayConfirm`
truncates each element without splitting on `\n`, and box height is computed as
`len(body)+2`. The result is raw newlines escaping the overlay and painting over
the sidebar. Also drop the duplicated title: `c.title` and `body[0]` are both
`sp.Label`.

**Acceptance criteria:**
- [ ] A body line containing `\n` is split into separate rendered lines
- [ ] Box height accounts for the split lines; nothing renders outside the border
- [ ] The action label appears once, in the title, not repeated as body line 0

**Verification:**
- [ ] `just test-one ConfirmBody`
- [ ] `K10S_SHOT_CONTEXT=k10s-demo-prod just shot 140 44 ":,p,g,c,enter,2"` — the
      manifest is inside the box
- [ ] `just check`

**Dependencies:** None
**Files:** `internal/ui/lens.go`, `internal/ui/view.go`, `internal/ui/lens_test.go`
**Scope:** S

---

### T2: Pooler severity table matches no real value

**Description:** `cnpg.yaml` grades `pooler-phase` with `ok: ["Pooler is ready"]`.
The real `Pooler.status.phase` enum is `active | paused | inactive | failed`
(CNPG 1.30), so every Pooler grades `warn` forever. Correct the table.

**Acceptance criteria:**
- [ ] `active` grades ok; `failed` grades error; `paused`/`inactive` grade warn
- [ ] An unrecognised value still grades warn, not ok

**Verification:**
- [ ] `just test-one CnpgPack`
- [ ] `just check`

**Dependencies:** None
**Files:** `internal/lens/builtin/cnpg.yaml`, `internal/lens/cnpg_pack_test.go` (new)
**Scope:** XS

### Checkpoint A
- [ ] `just check` passes
- [ ] Backup modal frame is inside its box

---

## Phase 2: Parameters — schema and store

### T3: `lens.Param` schema, parse, validate, `Vars.Params`

**Description:** Add a `Params []Param` field to `Action`. A `Param` has
`name`, `label`, `type` (`string|enum|int|bool`), `default`, `options []Option`,
`optionsFrom` (JSONPath or GVR), `allowFree`, `required`. Add
`Vars.Params map[string]string` so templates can reference `{{.Params.x}}`.
Validate: unique non-empty names, known type, `optionsFrom` parses as either a
JSONPath or a GVR, a non-`allowFree` default must appear in `options`.

**Acceptance criteria:**
- [ ] A pack declaring params parses; an invalid one is rejected with the param name in the error
- [ ] `Render("{{.Params.instance}}", vars)` resolves
- [ ] A missing param in a template is an error, not `<no value>`
- [ ] `Action.Check` reports a required param with no value

**Verification:**
- [ ] `just test-one Param`
- [ ] `just check`

**Dependencies:** None
**Files:** `internal/lens/lens.go`, `internal/lens/template.go`, `internal/lens/lens_test.go`, `internal/lens/template_test.go`
**Scope:** M

---

### T4: `lens.Kubectl` renders parameter values

**Description:** The preview command must reflect the parameters the operator
has filled in, for every verb. Fidelity here is a correctness property: the
rendered command has to describe the same mutation the request performs.

**Acceptance criteria:**
- [ ] Annotate, patch, status-patch, create and delete previews all substitute params
- [ ] An unfilled param renders its default, or an empty placeholder — never `<no value>`

**Verification:**
- [ ] `just test-one Kubectl`
- [ ] `just check`

**Dependencies:** T3
**Files:** `internal/lens/template.go`, `internal/lens/template_test.go`
**Scope:** S

---

### T5: Store resolves JSONPath suggestions; `LensAction` accepts params

**Description:** `domain.LensActionSpec` gains `Params []LensParamSpec` with
`Options []LensOption{Value, Note}` already resolved. The store evaluates
`optionsFrom` when it starts with `.` against the cached object, handling a
string list or a map (keys become values). `LensVerbs.LensAction` and
`LensPreview` take `params map[string]string`.

**Acceptance criteria:**
- [ ] `optionsFrom: .status.instanceNames` yields one option per instance
- [ ] `optionsFrom` against a map yields sorted keys
- [ ] A missing path yields no options and no error
- [ ] An informer that was never started yields no options without opening a watch
- [ ] `LensAction` renders templates with the supplied params

**Verification:**
- [ ] `just test-one LensParams`
- [ ] `just check`

**Dependencies:** T3, T4
**Files:** `internal/domain/*.go`, `internal/k8s/lensactions.go`, `internal/k8s/lensactions_test.go`, `internal/mock/lens.go`
**Scope:** M

---

### T6: Suggestions from a related kind via the edge table

**Description:** When `optionsFrom` parses as a GVR, resolve options by walking
the pack's existing `edges` to objects of that kind related to the selected row.
This is how "which Backup should I restore from?" is answered without inventing
a query language.

**Acceptance criteria:**
- [ ] `optionsFrom: postgresql.cnpg.io/v1/backups` on a Cluster lists that cluster's Backups
- [ ] Options carry a note (e.g. the backup's phase and finish time)
- [ ] A kind whose informer is not running yields no options and a stated reason

**Verification:**
- [ ] `just test-one LensParamsRelated`
- [ ] `just check`

**Dependencies:** T5
**Files:** `internal/k8s/lensactions.go`, `internal/k8s/lensedges.go`, `internal/k8s/lensactions_test.go`
**Scope:** M

### Checkpoint B
- [ ] `just check` passes
- [ ] A params pack round-trips through the store with options from a fake informer

---

## Phase 3: Parameters — UI

### T7: Form modal — state, focus, typeahead, key handling

**Description:** A `formState` holding the action spec, per-field buffers, the
focused field index, the highlighted option index and the filtered option list.
Tab / shift-tab move between fields; up / down move within suggestions; typing
filters; Enter accepts the highlighted option, or submits when the last field is
satisfied. Esc cancels. Filtering happens on keystroke, never in `View`.

**Acceptance criteria:**
- [ ] Typing filters the option list case-insensitively
- [ ] Enter on a highlighted option fills the field and advances
- [ ] A field with `allowFree: false` refuses a value not in its options
- [ ] Esc cancels without performing any write
- [ ] A required field left empty keeps the confirm button inert

**Verification:**
- [ ] `just test-one LensForm`
- [ ] `just check`

**Dependencies:** T5
**Files:** `internal/ui/lensform.go` (new), `internal/ui/update.go`, `internal/ui/model.go`, `internal/ui/lensform_test.go` (new)
**Scope:** M

---

### T8: Form modal — rendering

**Description:** Render the form: one labelled row per field, the focused field
showing its typeahead input and filtered suggestions beneath, each suggestion
with its note. Long option lists are capped with a count.

**Acceptance criteria:**
- [ ] Nothing renders outside the modal border at 140x44 and at 80x24
- [ ] The focused field is visually distinct from the rest
- [ ] An option's note renders in the subtle colour, right of its value

**Verification:**
- [ ] `just shot 140 44` and `just shot 80 24` of the fence form
- [ ] `just test-perf` — `View` stays off the row-building path
- [ ] `just check`

**Dependencies:** T7
**Files:** `internal/ui/view.go`, `internal/ui/lensform.go`
**Scope:** M

---

### T9: Live kubectl preview; typed-confirm against a parameter

**Description:** Recompute the preview from current parameter values as the form
changes, and let `confirmValue` name the string the operator must type — so
fencing asks for the *instance*, which is the value at risk, not the cluster name.

**Acceptance criteria:**
- [ ] Editing a field updates the preview in the same frame
- [ ] A multi-line preview stays inside the box (regression guard on T1)
- [ ] `confirmValue: "{{.Params.instance}}"` requires typing the instance name
- [ ] With no `confirmValue`, typed-confirm falls back to the object name as before

**Verification:**
- [ ] `just test-one LensFormPreview`
- [ ] `just check`

**Dependencies:** T7, T8
**Files:** `internal/ui/lensform.go`, `internal/lens/lens.go`, `internal/k8s/lensactions.go`
**Scope:** M

### Checkpoint C
- [ ] `just check` and `just test-perf` pass
- [ ] Fence form frame shows two real instances, one marked primary

---

## Phase 4: CNPG pack

### T10: Fence and promote migrate to params; promote gains its guards

**Description:** Replace `requiresSelection` on `cnpg-fence` and `cnpg-promote`
with an `instance` param sourced from `.status.instanceNames`, noted with role
from `.status.instancesReportedState`. Add the guards `kubectl cnpg promote`
applies before patching: refuse when the instance is already `targetPrimary`,
and when it is listed in `cnpg.io/fencedInstances`.

**Acceptance criteria:**
- [ ] Fencing offers the cluster's real instances, primary marked
- [ ] Promote refuses an already-target instance with that reason
- [ ] Promote refuses a fenced instance with that reason
- [ ] The typed confirmation asks for the instance name

**Verification:**
- [ ] `just test-one CnpgActions`
- [ ] `just check`

**Dependencies:** T9
**Files:** `internal/lens/builtin/cnpg.yaml`, `internal/lens/cnpg_pack_test.go`
**Scope:** S

---

### T11: Cluster columns — WAL archiving, node spread, image, certificates

**Description:** Add the columns that carry the signals an SRE actually checks.
`ContinuousArchiving` is the one that matters most: when it is `False`, WAL
archiving is broken and every backup is a lie.

**Acceptance criteria:**
- [ ] A `WAL` column reads the `ContinuousArchiving` condition status, graded error when `False`
- [ ] A `NODES` column reads `.status.topology.nodesUsed`, graded warn when it is 1 and instances > 1
- [ ] An `IMAGE` column reads `.status.image`
- [ ] A `CERTS` column reads the soonest `.status.certificates.expirations` value with `until` format
- [ ] Column count stays readable at 140 wide

**Verification:**
- [ ] `just test-one CnpgColumns`
- [ ] `just shot 140 44` of the PG Clusters table
- [ ] `just check`

**Dependencies:** T2
**Files:** `internal/lens/builtin/cnpg.yaml`, `internal/lens/cnpg_pack_test.go`
**Scope:** S

---

### T12: Backup action parameters

**Description:** `cnpg-backup` gains `method` (`barmanObjectStore` |
`volumeSnapshot` | `plugin`), `target` (`primary` | `prefer-standby`) and
`online`. Only emit fields the operator actually set — an empty `method` must
not be written, because `Backup.spec` is immutable after creation and a wrong
value cannot be edited, only recreated.

**Acceptance criteria:**
- [ ] Defaults produce exactly today's manifest, no extra keys
- [ ] Selecting `volumeSnapshot` offers `online`; selecting others does not write it
- [ ] Selecting `plugin` requires a plugin name and refuses without one

**Verification:**
- [ ] `just test-one CnpgBackupParams`
- [ ] `just check`

**Dependencies:** T9
**Files:** `internal/lens/builtin/cnpg.yaml`, `internal/lens/cnpg_pack_test.go`
**Scope:** M

---

### T13: New kinds — Database, Publication, Subscription, ImageCatalog

**Description:** Add the declarative CRDs CNPG shipped in 1.25+. Each has a
tiny status (`applied`, `message`), which is exactly what the table should show.

**Acceptance criteria:**
- [ ] Four kinds appear under the CloudNativePG group with short aliases
- [ ] `applied: false` grades error and surfaces `message`
- [ ] `ClusterImageCatalog` is declared cluster-scoped, `ImageCatalog` namespaced
- [ ] Edges connect each back to its Cluster via `.spec.cluster.name`

**Verification:**
- [ ] `just test-one CnpgKinds`
- [ ] `just check`

**Dependencies:** T2
**Files:** `internal/lens/builtin/cnpg.yaml`, `internal/lens/cnpg_pack_test.go`
**Scope:** M

---

### T14: Restore / PITR

**Description:** A `cnpg-restore` action creating a **new** Cluster with a
`bootstrap.recovery` stanza. Params: new cluster name, source Backup (from T6),
and an optional recovery target. Recovery target options are mutually exclusive
apart from `targetTLI`; setting two produces a cluster that will not bootstrap,
so the action refuses rather than sending it.

**Acceptance criteria:**
- [ ] The rendered manifest matches the documented `bootstrap.recovery` shape field-by-field
- [ ] Two mutually exclusive recovery targets are refused with that reason
- [ ] With no target set, the manifest omits `recoveryTarget` entirely
- [ ] The notice states plainly that this creates a NEW cluster and restores nothing in place
- [ ] Typed confirmation asks for the new cluster's name

**Verification:**
- [ ] `just test-one CnpgRestore`
- [ ] `just check`

**Dependencies:** T6, T9
**Files:** `internal/lens/builtin/cnpg.yaml`, `internal/lens/cnpg_pack_test.go`
**Scope:** M

---

### T15: Scale, resize, pooler scaling

**Description:** `.spec.instances` on a Cluster, `.spec.storage.size` on a
Cluster, `.spec.instances` on a Pooler — three int params, three merge patches.
Storage cannot shrink, so the action refuses a value below the current size.

**Acceptance criteria:**
- [ ] Scaling patches `.spec.instances` with the chosen integer, not a string
- [ ] Resize refuses a size smaller than the current one, stating that CNPG cannot shrink volumes
- [ ] Pooler scaling appears on the Pooler kind, not the Cluster

**Verification:**
- [ ] `just test-one CnpgScale`
- [ ] `just check`

**Dependencies:** T9
**Files:** `internal/lens/builtin/cnpg.yaml`, `internal/lens/cnpg_pack_test.go`
**Scope:** S

### Checkpoint D
- [ ] `just check` passes
- [ ] Every new action's preview reproduces its mutation, checked against the CNPG docs

---

## Phase 5: Evidence

### T16: Demo rows for every CNPG kind

**Description:** `internal/mock/lens.go` has rows only for `cnpg-clusters`, so
Backups, Scheduled Backups, Poolers and the new kinds cannot be screenshotted.
Add fixtures, worst-first, exercising the new severity grading.

**Acceptance criteria:**
- [ ] Every CNPG kind has demo rows
- [ ] Rows are ordered worst-first, matching how the real backend sorts
- [ ] Fixtures exercise a failing WAL archive and a single-node cluster

**Verification:**
- [ ] `just shot` of each CNPG kind
- [ ] `just check`

**Dependencies:** T11, T13
**Files:** `internal/mock/lens.go`
**Scope:** S

---

### T17: Documentation

**Description:** Document the `params` schema in `docs/lenses.md` and rewrite
the cnpg section to match what the pack now does.

**Acceptance criteria:**
- [ ] `params`, `optionsFrom` and `confirmValue` are documented with examples
- [ ] The cnpg section lists every kind and action, with the CNPG doc URL for each annotation
- [ ] Screenshots come from the binary

**Verification:**
- [ ] `just check`

**Dependencies:** T16
**Files:** `docs/lenses.md`, `CHANGELOG.md`
**Scope:** S

---

### T18: GIF evidence and PR

**Description:** Record the fence form and the restore form via `just demo`,
open the PR against `main` with the GIF embedded.

**Acceptance criteria:**
- [ ] GIF shows typeahead filtering, an option being picked, the preview updating
- [ ] PR body states what was fixed, what was added, and what was deliberately left out
- [ ] `just check` green on the branch

**Dependencies:** T17
**Files:** `assets/`, PR body
**Scope:** S

### Checkpoint E
- [ ] All acceptance criteria met
- [ ] GIF attached, ready for review
