# Lenses

A **lens** is a declarative pack that teaches k10s one ecosystem operator:
which custom resources it owns, what belongs in a table row, and which daily
actions apply. ArgoCD, Kargo, CNPG, Longhorn and Traefik shipped first; k3s
Helm, k3s system-upgrade, Fleet, Rancher and VictoriaMetrics followed. Ten lens
files plus one Go mechanism — not ten features.

The evidence behind every design choice is in [Why this shape](#why-this-shape).
Read that before changing the schema; most of the obvious simplifications
are wrong for a specific, documented reason.

## The problem this replaces

`internal/k8s/kinds.go` holds a static `builtinKinds` slice, and the one
generic escape hatch — `customresources` — is special-cased in five files
(`actions.go`, `counts.go`, `describe.go`, `rows.go`, `store.go`). Its
implementation (`Store.refreshCRs` in `rows.go`) lists **every CRD on the
cluster every 15 seconds**. On a cluster running ArgoCD, CNPG, Longhorn and
Traefik that is dozens of live LISTs on a timer, and the resulting table has
three columns: NAME, KIND, AGE.

Adding five operators as static kinds would mean ~20 new entries and five
new branches in each of those five files. Adding them as lenses means one
new mechanism and five YAML files — and *fewer* API calls than today,
because a lens names its exact GVRs and gets a dynamic informer per GVR
instead of a poll sweep over everything.

## Schema

A lens is YAML. Built-in lenses are embedded in the binary; users drop their
own in `~/.k10s/lenses/*.yaml` (override by `name`), alongside the existing
`plugins.yaml`.

```yaml
name: argocd
requires:                     # discovery gate — the whole pack is invisible
  - argoproj.io/v1alpha1      # unless every listed group/version is served
kinds:
  - key: argocd-apps
    name: Applications
    short: app
    group: ArgoCD             # the Resources-pane section header
    gvr: argoproj.io/v1alpha1/applications
    namespaced: true
    columns:
      - {header: NAME,     path: .metadata.name}
      - {header: SYNC,     path: .status.sync.status,   severity: sync}
      - {header: HEALTH,   path: .status.health.status, severity: health}
      - {header: REVISION, path: .status.sync.revision, truncate: 7}
      - {header: PROJECT,  path: .spec.project}
      - {header: AGE,      path: .metadata.creationTimestamp, format: age}
    actions: [describe, yaml, argocd-sync, argocd-refresh, argocd-rollback]
```

### Columns

`path` is a JSONPath over the unstructured object. `format` is one of `age`
(RFC3339 → `4d2h`), `bytes`, `int`, `bool`, or absent (verbatim string).
`truncate: N` cuts to N runes — git revisions want 7.

**Filter paths work, and you will need one.** The k8s JSONPath dialect supports
the composite filter form, so the usual condition list reads as one cell:

```yaml
- {header: READY, path: '.status.conditions[?(@.type=="Ready")].status', severity: node-ready}
- {header: MESSAGE, path: ".status.conditions[?(@.message!='')].message", truncate: 44}
```

Quote the whole scalar in YAML and use the *other* quote inside the filter —
`'…[?(@.type=="Ready")]…'` or `"…[?(@.message!='')]…"`. Most CRDs in this
release publish no scalar status field at all, only conditions, so this is the
only way to get a column out of them.

A path may match **zero, one or many** entries. Zero renders an empty cell,
which is never graded and never an error — identical to a missing field. Many
render **space-joined in declaration order** (`.status.conditions[*].type` →
`LatestResolved Complete Validated`), with `truncate` applied to the joined
string, not per match. So filter down to one match whenever the column is
graded by a `severity` table: a two-match cell is a string no table lists and
grades as `unknown`.

`severity` names a mapping table that drives **both** the row colour and the
sort comparator. Four levels: `ok`, `warn`, `error`, `unknown`. A kind
declaring any severity column sorts **worst-first by default**, so
`Degraded` and `CrashLoopBackOff` float to the top instead of scattering
alphabetically among healthy rows.

```yaml
severities:
  health:
    ok:      [Healthy]
    warn:    [Progressing, Suspended]
    error:   [Degraded, Missing]
    unknown: [Unknown]
  cnpg-phase:
    ok:      ["Cluster in healthy state"]   # CNPG phases are sentences
    default: warn                           # anything unlisted
```

`default:` matters more than it looks. CNPG's `.status.phase` is a human
sentence (`"Waiting for user action"`, `"Cluster is unrecoverable and needs
manual intervention"`), not a slug — enumerating them all is a losing game,
so one value is `ok` and everything else is `warn`.

### Actions

Five verbs cover every daily action across all ten operators — the five packs
added since needed no eleventh verb, only `patch`. Nothing here
shells out; nothing here needs the operator's own CLI, gRPC API or HTTP API.

| Verb | What it does | Used by |
| --- | --- | --- |
| `annotate` | set/remove annotations on the object | ArgoCD refresh, all four Kargo verbs, five CNPG actions |
| `patch` | JSON merge patch on the object | ArgoCD sync, Longhorn spec fields, Traefik middleware refs |
| `status-patch` | JSON merge patch on `/status` | CNPG promote, Kargo approve-freight |
| `create` | create a templated object | Kargo Promotion, CNPG Backup, Longhorn Snapshot/Backup |
| `delete` | delete a named related object | Longhorn detach (delete an attachment ticket) |

```yaml
actions:
  - id: cnpg-fence
    label: Fence instance
    verb: annotate
    confirm: "true"            # QUOTED: the field is a string, and an
                               # unquoted true is a YAML bool that fails to
                               # parse and takes the whole pack with it
    requiresSelection: true     # the value is an instance, not the Cluster
    annotations:
      cnpg.io/fencedInstances: '["{{.Selected}}"]'   # JSON array, quoted

  - id: cnpg-promote
    label: Promote to primary
    verb: status-patch
    confirm: typed              # requires typing the object name
    requiresSelection: true
    patch:
      status:
        targetPrimary: "{{.Selected}}"
        targetPrimaryTimestamp: "{{.Now}}"
        phase: Switchover in progress
    retryOnConflict: true

  - id: kargo-refresh
    label: Refresh
    verb: annotate
    annotations:
      kargo.akuity.io/refresh: "{{.Now}}"
    ack: .status.lastHandledRefresh   # spinner clears when this equals what we wrote

  - id: argocd-sync
    label: Sync
    verb: patch
    confirm: "true"            # QUOTED: the field is a string, and an
                               # unquoted true is a YAML bool that fails to
                               # parse and takes the whole pack with it
    notice: >-                  # shown verbatim in the modal
      Writing .operation bypasses argocd-rbac-cm entirely.
    refuseWhen:                 # checked before the round trip
      - path: .operation
        reason: another operation is already in progress
    patch:
      operation: {sync: {syncStrategy: {apply: {}}}}
```

### Selection, notices and preconditions

Three fields exist because the verb alone cannot express what they say.

**`requiresSelection`** marks an action whose `.Selected` cannot fall back to
the row's own name. Most can — fencing instance `my-db` reads the same either
way — but a CNPG *instance* is a pod called `my-db-2`, a Longhorn backup needs
a *snapshot* name, a Kargo promotion needs a *Freight* name, and an ArgoCD
rollback needs a *git revision*. None of those is the row's name, and
substituting it would target the wrong object or, worse, be silently ignored
(the Kargo controller drops a re-verify with an empty id, which looks like
success). The backend reports such an action **disabled with a reason**, and
the UI turns that one reason into a question — pressing it asks which
instance, then runs with the answer. The answer is scoped to the row it was
given for: carrying it to the next row would let the second press skip the
question entirely and act on an instance belonging to the first.

**`target`** redirects the write to a SIBLING object of a different GVR,
keeping the selected row's name and namespace. Longhorn attach/detach is the
whole reason it exists: the state lives on a `VolumeAttachment` CR named
identically to the `Volume` you are looking at, so "patch a different kind,
same name" is the mechanism rather than a special case.

An earlier draft of this document used `{{.Name}}` for CNPG fence and promote.
That was wrong for exactly this reason, and the packs are correct where they
differ from it.

**`notice`** is free text the confirm modal prints verbatim. Some warnings are
facts about the operator rather than about the verb — that syncing an ArgoCD
Application by writing `.operation` bypasses `argocd-rbac-cm` is not derivable
from "this is a patch". Without a field, the disclosure could only live in a
YAML comment the parser throws away, or as a Go constant keyed on an action
id — a test asserting that a string equals itself.

**`refuseWhen`** blocks the action when any listed JSONPath resolves to a
non-empty value, with the reason shown instead. ArgoCD refuses a sync while
`.operation` is set and fights one against an automated sync policy; k10s
should refuse before the round trip rather than relay the server's complaint.

`ack` is the reason this is a mechanism and not five ad-hoc buttons.
Annotation-as-verb is the dominant integration surface across the ecosystem,
and three of these five operators publish an acknowledgement field. One
generic "write the annotation, watch the ack, clear the spinner" primitive
serves all of them.

`confirm` has three levels: absent (act immediately), `true` (modal showing
the equivalent `kubectl` command), and `typed` (must type the object's
name). Destructive and failover actions use `typed`.

Template variables: `.Name`, `.Namespace`, `.Context`, `.Now` (RFC3339),
`.Selected` (the highlighted sub-row — a chosen instance or revision).

## Relationships

Each lens may declare edges. This is the part no terminal UI has.

```yaml
edges:
  - from: v1/pods
    to: postgresql.cnpg.io/v1/clusters
    via: label
    key: cnpg.io/cluster
```

`via` is `label` (match a label value against the target's name),
`ownerRef`, `annotation`, or `field` (a JSONPath on either side). Edges are
bidirectional for navigation: from a pod, walk *up* to the CNPG Cluster that
owns it, *across* to its latest Backup, *down* to the PVC, across again to
the Longhorn Volume backing it, and finally to the node holding a degraded
replica.

`ownerRef` alone cannot express any of those hops, which is exactly why the
edge table is data. Headlamp reached the same conclusion and made Resource
Map relationships plugin-defined in v0.45.

## In the TUI

A lens kind is an ordinary table: same navigation, same search, same zoom.
Four things are specific to it.

**Severity colour.** A column that names a `severity` table is coloured from
that table rather than from the built-in status guesses. The vocabulary is
`ok` / `warn` / `error` / `unknown`; `unknown` is drawn subtle, not red,
because "the operator has not said" is not a failure. An **empty cell is
never graded** — half the status fields in these CRDs are optional, and
grading absence would paint every idle Application's `OPERATION` column.

**Verbs on the digits.** A lens kind's actions are listed under their own
rule in the Actions pane, keyed `1`–`9`. Digits, because every useful letter
is already a built-in action or a plugin shortcut. An action you cannot
currently run is **listed disabled with its reason** rather than hidden, and
pressing its digit says the reason out loud — a button that vanishes is a
mystery, and one that fires against the wrong object is worse.

**Two kinds of gate.** `confirm: "true"` is the ordinary modal. `confirm:
typed` requires the object's own name to be typed before Enter or the OK
button does anything, and is used for writes no controller can undo. Both
modals end with the equivalent `kubectl` line: it is how an operator checks
that the button does what they think, and how they reproduce it in a runbook
afterwards. An action declaring `requiresSelection` **asks** which instance
to act on instead of refusing — a CNPG instance is `my-db-2`, never the
cluster's own name, so there is nothing to default to.

**`R` shows relationships.** One hop, in both directions, in the same text
panel describe and YAML use. A neighbour whose kind has never been opened is
listed and marked *not loaded*: that is a different answer from "does not
exist", and only one of them is fixed by opening that kind.

Waiting is visible. An action declaring `ack` spins in the pane until the
controller echoes the token back into that field. Timing out is reported as
`sent, no acknowledgement yet` — not as a failure, because the write did go
through and saying otherwise sends the operator to retry a queued request.

The offline demo carries these kinds on the `k10s-demo-prod` context only,
gated the way discovery gates the real thing, with columns and verbs read
from the shipped packs rather than a second copy:

```
K10S_SHOT_CONTEXT=k10s-demo-prod just shot 150 40 ':,a,r,g,o,c,d,-,a,p,p,s,enter'
```

## Security disclosures

Two of these are not incidental — they belong in the UI, not only in this
file.

**ArgoCD sync bypasses ArgoCD's own RBAC.** `argocd-rbac-cm` (`policy.csv`)
is enforced by `argocd-server`. Writing `.operation` directly on an
Application is a plain Kubernetes update that the Argo policy engine never
sees — effectively "sync anything" for any principal holding `update` on
`applications`. The sync confirm modal states this. Operators who need Argo
policy enforced should deny `update` on `applications` and use the Argo CLI.

**Kargo promote needs a virtual verb.** The validating webhook issues a
SubjectAccessReview for the verb `promote` on `stages`. Plain `create` on
`promotions` is *not* sufficient, and the failure arrives as a webhook
rejection at create time rather than a 403 on the thing you clicked. Surface
the webhook message verbatim.

## The shipped lenses

### argocd — ship first

`argoproj.io/v1alpha1` only, three kinds (`applications`, `applicationsets`,
`appprojects`), all namespaced. `.status.sync.status` and
`.status.health.status` are the upstream printer columns, so the row is
designed for us.

Every daily action is a plain Kubernetes write: **sync** = set top-level
`.operation` (Application has no status subresource, so one `update` does
it); **refresh** = annotate `argocd.argoproj.io/refresh: normal|hard`;
**terminate** = set `.status.operationState.phase = Terminating`;
**rollback** = sync with a revision picked from `.status.history[]`.

Refuse sync when `.operation != nil` (`ErrAnotherOperationInProgress`) and
when `.spec.syncPolicy.automated` is set. Never hardcode `-n argocd` —
apps-in-any-namespace has been GA since 2.5, so list cluster-wide and key on
`<ns>/<name>`.

Two limits the view states plainly: **diff is API-server-only** (it needs
repo-server gRPC), so k10s shows sync state, not a manifest diff; and when
`.status.resourceHealthSource == appTree`, per-resource health lives in
Redis and `.status.resources[].health` is empty.

`AppProject.spec.syncWindows[]` is enforced by the controller, not a
webhook — a sync written during a closed window is accepted and then
silently not executed. The row shows window state so that is not a mystery.

### cnpg — ship second

`postgresql.cnpg.io/v1`, one served version. The row is
`readyInstances/instances` + `currentPrimary` + phase severity. Actions are
one annotation or one status patch each: **fence**
(`cnpg.io/fencedInstances`, a JSON array string), **hibernate**
(`cnpg.io/hibernation: on|off`), **restart**
(`kubectl.kubernetes.io/restartedAt`), **reload** (`cnpg.io/reloadedAt`),
**backup** (create a `Backup` CR), **promote** (status patch,
conflict-retry).

This is the only one of the five with a genuine standalone types module —
`github.com/cloudnative-pg/api` — but the lens mechanism uses unstructured
uniformly, so consistency wins over typed structs.

Backup recency does **not** come from `.status.lastSuccessfulBackup`; that
whole family (`firstRecoverabilityPoint`, `lastSuccessfulBackupByMethod`,
`lastFailedBackup`, …) is deprecated because backup moved to the CNPG-I
plugin. Derive it from `Backup` CRs (`.status.stoppedAt`, `.status.phase`).

Do not scrape port 8000: since 1.30 the instance-manager's sensitive
endpoints require the operator's pinned ECDSA client cert, and a TUI hitting
it looks like an attacker. `.status` is sufficient.

### longhorn — scope tightly

`longhorn.io/v1beta2` only (`v1beta1` was removed in 1.10.0). 25 kinds
exist; the lens declares **four**: `volumes`, `nodes`, `backups`,
`snapshots`.

The volume row — `state` + `robustness` + `currentNodeID` +
`.status.kubernetesStatus.{pvcName,workloadsStatus[]}` — is a better storage
view than kubectl offers, because it carries the backref to the workload
actually using the disk.

Declarative actions: snapshot (create a `Snapshot` CR with
`spec.createSnapshot: true`), backup (create a `Backup` CR — **the label
`longhorn.io/backup-volume: <vol>` is mandatory**, controllers select on
it), detach (patch `spec.attachmentTickets` on the `VolumeAttachment` CR via
`target`, which is named identically to the volume), and node scheduling.

**Shipped so far:** snapshot, backup, detach, node enable/disable
scheduling. **Not shipped:** attach and replica-count. Attach needs a
ticket *id* the UI has no way to obtain — `requiresSelection` would be
asking the operator to invent one — and changing `spec.numberOfReplicas`
triggers a rebuild whose cost the row does not show. Both are honest
omissions rather than oversights, and neither is listed as an action in
`longhorn.yaml`.

Deliberately **not** implemented: trim, salvage, snapshot-revert, engine
upgrade. Those are genuinely imperative and exist only on the HTTP API at
`:9500`, which `networkPolicies.restrictInternalTraffic` blocks by default
in 1.12 and which has no authentication of its own. Every action in the lens
inherits kubeconfig RBAC and lands in the audit log; that property is worth
more than four extra buttons.

Scale is the real constraint: one volume is 1 Volume + 1–2 Engines + N
Replicas + 1 VolumeAttachment + one Snapshot CR per snapshot, including
system and rebuild snapshots. 500 volumes ⇒ 10k+ objects. Informers only,
never poll-list; select replicas by label `longhornvolume: <name>`. Do not
hardcode `longhorn-system`.

The snapshot table shows a `USER` column rather than filtering to
`userCreated: true`, because the schema has no filter concept: a column can
be read and sorted on, but a pack cannot hide rows. Filtering would also be
the wrong default — a rebuild snapshot is exactly what you want to see when
a replica is rebuilding.

### kargo

`kargo.akuity.io/v1alpha1`, nine kinds; the lens declares `stages`,
`freights`, `promotions`, `warehouses`. All five daily actions are plain
k8s: **promote** (create `{generateName: promo-, spec:{stage, freight}}` and
let the mutating webhook inflate the steps — never build `spec.steps`
client-side), **approve freight** (status patch on `freights/status`, not an
annotation), **refresh**, **abort**, **re-verify** (annotations).

Two traps that older material gets wrong: `Stage.status.currentFreight` and
`Stage.status.phase` **do not exist** — use `.status.freightSummary`, which
is purpose-built for a table column, plus `.status.health.status`. And
`Freight` has no `spec` at all; `alias`, `origin`, `commits` and `images`
are top-level.

Promotion names are `<stage>.<ULID>.<hash>`, so lexical sort is
chronological for free. `Project` is cluster-scoped and reconciles to a
same-named namespace labelled `kargo.akuity.io/project: "true"` — that is
the project picker, no extra API needed.

### traefik — lowest value, and honestly labelled

`traefik.io/v1alpha1` (the `traefik.containo.us` group was removed in v3).
Ten kinds; the lens declares `ingressroutes`, `middlewares`,
`traefikservices`.

**These CRDs have no `.status` field at all.** Not a thin status — none. No
conditions, no events, no printer columns; the Traefik ClusterRole carries
`get,list,watch` and nothing else on them. A view built on the Kubernetes
API can therefore only re-display spec, which is a prettier
`kubectl get -o yaml`.

The one genuinely valuable signal — *your IngressRoute is `disabled`
because the match rule is malformed* — exists only in the Traefik HTTP API
(`/api/http/routers`, where `status` is `enabled|disabled|warning` and
`error[]` carries the reason). In the official Helm chart that API is not
exposed outside the pod (`ingressRoute.dashboard.enabled: false`,
`expose.default: false`, no Service port for 8080), so it is reachable only
through a port-forward the user explicitly starts. k10s already has
port-forward.

So: the Traefik lens is spec-only by default, with an **opt-in router
inspector** that port-forwards the API and joins CRD rows to live routers on
the `<ns>-<name>-<hash>@kubernetescrd` naming convention. It is not a
headline feature and this file will not pretend otherwise.

**If you want a first-class ingress view, build it on Gateway API instead.**
`gateway.networking.k8s.io/v1` has exactly what the Traefik CRDs lack:
`Gateway.status.conditions` (Accepted, Programmed),
`.status.listeners[].attachedRoutes`,
`HTTPRoute.status.parents[].conditions`. Traefik v3 already implements it,
and one Gateway API lens covers Traefik, Istio, Envoy Gateway and Cilium at
once. Recommended as a follow-up card.

### k3s-helm

`helm.cattle.io/v1`, two kinds: `helmcharts` (`hc`) and `helmchartconfigs`
(`hcc`), both namespaced, both read-only (describe/yaml/edit).

`HelmChartStatus` has **no scalar fields** — no `failed`, no `jobCreated`, no
timestamp anywhere, not even inside a condition — so FAILED is a filter path on
`conditions[?(@.type=="Failed")].status` and there is no "last sync" column.
`HelmChartConfig` has no status subresource at all. Helm release revision and
deployed version live in the release Secret, not on either CR, so VERSION is
labelled as the *requested* `.spec.version` and nothing pretends otherwise.

`delete` is deliberately absent: it runs a helm-delete Job that uninstalls the
release while k3s's manifest controller recreates the CR from
`/var/lib/rancher/k3s/server/manifests` — the destructive half sticks and the
visible half reverts.

Edges: `helmcharts → batch/v1/jobs` via field `.status.jobName` (read, never
reconstructed — it flips to `helm-delete-<name>` on deletion),
`pods → helmcharts` via label `helmcharts.helm.cattle.io/chart`, and
`helmchartconfigs → helmcharts` via `.metadata.name`.

### k3s-upgrade

`upgrade.cattle.io/v1`, one kind: `plans` (`plan`), namespaced, read-only.

`PlanStatus` is exactly conditions + `latestVersion` + `latestHash` +
`applying`. No job name, no timestamps — the only times on the object are
inside conditions. LATEST is `.status.latestVersion`, which the controller
resolves from either `.spec.channel` or `.spec.version`, so `.spec.version`
gets no column of its own.

Edges: `plans → jobs` via label `upgrade.cattle.io/plan`, `jobs → nodes` via
label `upgrade.cattle.io/node`, so `R` twice walks plan → failing job → stuck
node. The narrower `plan.upgrade.cattle.io/<name>=<latestHash>` key is
undeclared on purpose: it embeds the plan name, so it cannot be a static edge
key, and it would hide exactly the leftover failed-version Jobs you came for.

### fleet

`fleet.cattle.io/v1alpha1`, three kinds: `gitrepos`, `bundles`,
`bundledeployments` — the GitRepo → Bundle → BundleDeployment chain and nothing
else of Fleet's dozen CRDs.

**A label key in a path needs its dots backslash-escaped:**
`.metadata.labels.fleet\.cattle\.io/repo-name` resolves;
`.metadata.labels['fleet.cattle.io/repo-name']` renders empty, because the
parser splits the bracketed key on its own dots.

Edges are child → parent by label — `bundles → gitrepos` on
`fleet.cattle.io/repo-name`, `bundledeployments → bundles` on
`fleet.cattle.io/bundle-name`. Caveat carried in the YAML: the key can express
only the name half of the verified name+namespace pair, so same-named Bundles
in two namespaces over-match.

Actions are `fleet-pause` / `fleet-resume`, a merge patch on `spec.paused` with
a literal YAML bool — `RenderTree` only templates string leaves. Force-resync
is not shipped for the same reason: `.spec.forceSyncGeneration` is an `int64`
that must be written as current+1, and a templated leaf would send `"3"`.

### rancher

Two group/versions, so the pack appears only on a Rancher **management**
cluster: `management.cattle.io/v3` (`clusters`, short `mcluster`) and
`catalog.cattle.io/v1` (`clusterrepos`, short `crepo`). Both cluster-scoped.

One action: `rancher-repo-refresh`, patching `.spec.forceUpdate` with
`{{.Now}}` (it is a `*metav1.Time`), confirmed, with a notice that it re-pulls
a possibly large git clone and an `ack` on `.status.downloadTime`.

No edges ship. Management Cluster → `provisioning.cattle.io/v1` clusters and
→ `clusters.fleet.cattle.io` are the obvious hops, and neither could be
verified — an unverified edge is a wrong answer with a confident face. Note the
name clash while you are here: `clusters` exists in the management, fleet and
provisioning groups and they are three different objects.

### victoriametrics

`operator.victoriametrics.com/v1beta1`, four kinds: `vmagents`, `vmalerts`,
`vmrules`, `vmservicescrapes`. STATUS is `.status.updateStatus` everywhere,
graded by one table over the five upstream constants (`operational` ok;
`expanding`/`paused`/`ignored` warn; `failed` error).

`.status.status` and `.status.lastSyncError` are the pre-v0.51.0 names and are
empty on any current operator, so they are not shown. PAUSED and the
pause/resume actions exist only on VMAgent and VMAlert, whose specs embed
`CommonAppsParams`; VMRule and VMServiceScrape have no such field to patch.
REPLICAS on VMAlert is `.spec.replicaCount` — desired, not observed, because
the status carries neither.

Edges are label edges on `app.kubernetes.io/instance`, from pods and from
apps/v1 deployments/statefulsets/daemonsets (a VMAgent runs as any of the
three). OwnerRef edges were tried first and dropped as unverifiable. That label
is generic enough to over-match on a busy namespace; the YAML says so.

## Dependencies

Zero new Go modules. Every lens uses the `dynamic` client and
`unstructured`, which `internal/k8s/client.go` already builds (`Dynamic`,
`Discovery`, `Mapper`).

That is not laziness, it is the only viable option for three of the five.
`longhorn-manager` requires `k8s.io/kubernetes` plus ~195 requires and **36
`replace` directives** for k8s.io staging repos — and a dependency's
`replace` directives do not apply to the importing module, so all 36 would
have to be copied or the build breaks on `v0.0.0` pseudo-versions. Traefik's
types package transitively imports its whole config and observability tree.
Importing ArgoCD's types drags in Helm and Kustomize. For a project whose
pitch is "single static binary", unstructured is correct.

## Why this shape

Load-bearing evidence, so a future change can check whether the reasons
still hold:

- **Extensible edges, not ownerRefs.** Headlamp made Resource Map
  relationships plugin-definable in v0.45 precisely because ownerRefs cannot
  express Crossplane composites, Gateway API or Helm ownership. k9s XRay
  walks downward only (deploy→rs→pod). Upward and lateral navigation is
  unoccupied ground in terminal UIs.
- **Severity-first sort.** k9s #3589 (sorting by STATUS scatters
  CrashLoopBackOff alphabetically among Running pods) was closed as *not
  planned*; k9s #3793 (CPU/MEM sort broken in pod view) is open. This is a
  comparator, not a feature.
- **Annotation-as-verb is the ecosystem's dominant write surface.** ArgoCD
  refresh, all four Kargo verbs and five CNPG actions are `kubectl annotate`
  underneath. Building the annotate+ack primitive once covers most of the
  total action surface across three of the five.
- **Confirm modals must show the kubectl equivalent, and must be rare.**
  The documented HITL failure mode is approval fatigue: high-frequency
  prompts train operators to auto-confirm. One good modal beats ten.
- **No official MCP server exists for any of the five.** `mcp-for-argocd` is
  semi-official and wraps the REST API; Kargo closed its MCP request as
  *not planned*; Helm's is an open proposal; Traefik's MCP work is a Hub
  gateway for *other* servers. Do not plan around MCP for these.

## Not in scope

- **Helm.** Releases are gzipped JSON in Secrets, not CRDs — a different
  mechanism, already tracked as card T19. Its blocker is RBAC: reading
  releases needs full `secrets` read (Kubernetes has no metadata-only verb)
  and the decoded blob contains every `--set` override, i.e. passwords.
- **Manifest diff** for ArgoCD (needs repo-server gRPC), and Traefik
  certificate state (`/api/certificates` is Traefik Enterprise only).
- **Writing lens packs from the UI.** They are files; edit them in an
  editor.
