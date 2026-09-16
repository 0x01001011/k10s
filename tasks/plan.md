# Implementation Plan: CNPG lens — parameterised actions, honest columns, PITR restore

## Overview

The CNPG lens currently exposes eight actions behind a confirm modal that
renders a raw JSON blob, prompts for an instance name as free text, and reads
seven of the ~30 status fields CloudNativePG publishes. This plan turns it
into a day-2 Postgres console: actions gain declarative **parameters** with
typeahead suggestions sourced from live cluster state, the table starts
reporting the signals that actually page an SRE (WAL archiving, node spread,
certificate expiry), and the four CRDs CNPG added in 1.25+ become browsable.

The parameter mechanism is the centre of gravity. It is deliberately *not* a
CNPG feature: it is one addition to the lens schema that every pack can use,
and it is what makes restore/PITR expressible at all.

## Tracker note

The repo designates GitHub Issues as its tracker (`docs/agents/issue-tracker.md`),
but that is for issues and specs filed for humans to pick up. This is a single
authored PR, so tasks live in `tasks/todo.md` — the file `/build` reads — rather
than as eighteen new GitHub issues nobody asked for. If any task here turns out
to be a bug worth tracking independently, file it then.

## Architecture Decisions

### 1. `params:` on an Action, not a CNPG special case

An action may declare parameters. Each has a name, a label, an optional
default, and an optional source of suggestions. Templates reference them as
`{{.Params.<name>}}`, alongside the existing `.Name`, `.Namespace`, `.Now`.

Rationale: the current `requiresSelection` / `.Selected` pair is a
single-parameter special case with a hardcoded prompt. Fencing needs one
instance; backing up needs a method *and* a target *and* an online flag;
restoring needs a name, a source backup and a recovery target. One parameter
slot cannot express the last two, and three more bespoke fields would be three
more mechanisms.

### 2. Suggestions resolve in `internal/k8s`, never in `internal/ui`

`optionsFrom` is a JSONPath (leading `.`) evaluated against the selected object
in the informer cache, **or** a GVR (`group/version/resource`) resolved through
the pack's existing `edges` table to the names of related objects.

Rationale: the UI already never parses JSONPath, resolves a GVR or touches a
lister — `domain.LensActionSpec` arrives pre-computed. Reusing the edge table
for the GVR case rather than adding a query language is the smaller change:
"which Backups belong to this Cluster" is already declared in `cnpg.yaml`.

### 3. `.Selected` stays; CNPG migrates off it

`requiresSelection` is used by argocd, kargo and longhorn as well as cnpg.
This PR migrates only the two CNPG actions to `params` and leaves the
mechanism intact for the other three packs. Deleting `requiresSelection`
outright is the correct end state and is a follow-up PR, because it touches
three packs outside this PR's scope and their tests.

### 4. Restore creates a new Cluster and says so loudly

CloudNativePG never restores in place; recovery is always a *new* `Cluster`
with a `bootstrap.recovery` stanza. The action is therefore `verb: create`
targeting `clusters`, with a `notice` stating this plainly, `confirm: typed`
against the new cluster's name, and the full rendered manifest in the preview.

### 5. Live kubectl preview is a correctness feature, not decoration

The modal already shows the equivalent kubectl command. With parameters the
command changes as the operator fills the form, so the preview must be
recomputed per keystroke via a new `LensPreview` call. An operator who cannot
see what the button will do cannot check it, and restore is the action where
that matters most.

## Dependency graph

```
T1,T2  modal bugs + severity fix        (independent, land first)
   |
T3  lens.Param schema + Vars.Params
   |
   +-- T4  Kubectl renders params
   |
   +-- T5  store resolves optionsFrom (jsonpath) + LensAction takes params
          |
          +-- T6  optionsFrom via GVR + edges
          |
          +-- T7  form state + keys -- T8  form rendering -- T9  live preview
                                                   |
                                        T10..T15  CNPG pack work
                                                   |
                                        T16 demo - T17 docs - T18 GIF
```

## Task List

See `tasks/todo.md`.

### Phase 1: Bugs (no new mechanism)
- T1 Multi-line kubectl breaks the confirm modal
- T2 Pooler severity table matches no real value

### Checkpoint A
- `just check` passes; `just shot` of the backup modal is inside its box

### Phase 2: Parameters — schema and store
- T3 `lens.Param`, parse, validate, `Vars.Params`
- T4 `lens.Kubectl` renders parameter values
- T5 Store resolves JSONPath suggestions; `LensAction` accepts params
- T6 Suggestions from a related kind via the edge table

### Checkpoint B
- `just check` passes; a pack declaring params round-trips through the store
  with options populated from a fake informer

### Phase 3: Parameters — UI
- T7 Form modal: state, focus, typeahead filtering, key handling
- T8 Form modal: rendering
- T9 Live kubectl preview; typed-confirm against a parameter value

### Checkpoint C
- `just check` passes; `just shot` shows the fence form with two real
  instance suggestions, one marked primary

### Phase 4: CNPG pack
- T10 Fence + promote migrate to params; promote gains its real guards
- T11 Cluster columns: WAL archiving, node spread, image, certificate expiry
- T12 Backup action parameters: method, target, online
- T13 New kinds: Database, Publication, Subscription, ImageCatalog
- T14 Restore / PITR
- T15 Scale instances, resize storage, pooler scaling

### Checkpoint D
- `just check` passes; every new action's kubectl preview reproduces the
  mutation exactly, verified against the docs

### Phase 5: Evidence
- T16 Demo rows for every CNPG kind
- T17 `docs/lenses.md`: params schema + rewritten cnpg section
- T18 GIF via `just demo`; PR

### Checkpoint E
- All acceptance criteria met; GIF attached; ready for review

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Restore template is wrong and creates a broken cluster | **High** | `confirm: typed`; full manifest in the preview; notice stating it creates a NEW cluster; unit test asserts the rendered manifest field-by-field against the documented `bootstrap.recovery` shape |
| PITR recovery targets are mutually exclusive (all but `targetTLI`) — a form that sets two produces a cluster that will not bootstrap | **High** | Validate at parse time and again before the write; refuse with the reason rather than sending it |
| `LensAction` signature change ripples through mock + tests | Medium | Change the interface once in `domain`, fix `internal/mock` in the same commit; compiler finds every caller |
| Form modal is the largest new UI surface and `View` is on a perf guard (`just test-perf`) | Medium | Form state is computed on keystroke, never in `View`; mirror the existing `lensActions()` memo pattern; run `just test-perf` at Checkpoint C |
| `optionsFrom` reads an informer that was never started, so suggestions are silently empty | Medium | `cachedLensObject` already returns nil rather than opening a watch; surface "no suggestions — open that kind to load them" in the form instead of an empty list |
| Suggestion list from a related kind could be large (hundreds of Backups) | Low | Typeahead filters; cap the rendered list and show a count |
| Deprecated `.status.lastSuccessfulBackup` family is empty under the Barman plugin | Medium | Derive backup freshness from `Backup` objects, as the current pack comment already says; do not add a column reading the deprecated fields |

## Open Questions

- Should `params` support a `secret` type (masked input)? Nothing in the CNPG
  scope needs one. Deferring until something does.
- Replica-cluster promotion needs `.spec.replica.promotionToken`, whose value
  comes from the *other* cluster's `.status.demotionToken` — possibly in another
  kubeconfig context. Out of scope for this PR; a token-less promotion silently
  degrades to a failover requiring a rebuild, so it must not be offered as a
  one-key action until the token can be sourced safely.
