# SPEC — k10s main-view redesign

Status: draft, not implemented. Scope: `internal/ui` centre pane and the
contracts around it. Produced from a five-expert audit of the shipped binary
(frames captured with `just shot`, every claim traced to `file:line`).

Companion backlog cards: `docs/plan.md`, section **P5**, cards **T37–T45**.

| Module | Card |
|---|---|
| M1 frame memo | T37 |
| M2 perf guards | T38 |
| M3 layout budget | T39 |
| M4 column policy | T40 (weight/priority/width) + T41 (honest columns) |
| M5 sort | **T07**, already in the backlog — this spec only fills in its open decisions |
| M6 row groups | T42 |
| M7 view engine | T43 |
| M8 history + charts | T44 |
| M9 actions + gates | T45 |

---

## 1. Objective

The centre pane is one hardcoded table. Every view k10s wants next — grouped
rows, an owner tree, a metrics chart, a port-forward list — has nowhere to
live, so each would grow another branch inside a 1419-line `view.go` and a
2946-line `model.go`.

Replace it with a **view engine**: the centre pane renders one of several
view modes over a single, still-flat row contract. Then ship the views the
audit says are missing.

Target users: cluster operators who live in a terminal, at 80×24 as often as
at 160×48.

**Success criteria**

1. At 80×24, a 14-pod namespace shows every pod with NAME untruncated and
   STATUS unabbreviated. Today: 12 rows, `web-frontend-6b8c…`, `x Crash…`.
2. Pods group under their owner by default, and sorting, filtering, row
   numbers and selection behave exactly as they do today.
3. A per-row CPU sparkline and a per-object CPU/MEM chart exist, built from
   samples that the render path never writes.
4. Every verb in the app is findable by typing its name.
5. `just test-perf` still passes, and the frame budget in §6 holds.

**Non-goals.** Migration to bubbletea/lipgloss v2. Drag-to-resize splits.
A tab bar. Animation. Replacing the hand-rolled table with `bubbles/table`
(reasons in §8).

---

## 2. Capability map

Nine modules. Dependency direction is strictly downward; build order is the
numbering.

| id | Module | Depends on | Lane |
|---|---|---|---|
| **M1** | Frame memo — one `Rows()` per frame | — | perf |
| **M2** | Perf guards that measure navigation | — | perf |
| **M3** | Layout budget — responsive header + panes | — | layout |
| **M4** | Column policy — weight, priority, display-width | M1 | table |
| **M5** | Sort (implements card T07) | M1, M4 | table |
| **M6** | Row groups + owner grouping | M1, M4, M5 | table |
| **M7** | View engine — the mode switch | M3, M6 | engine |
| **M8** | Metric history + charts | M1, M7 | viz |
| **M9** | Action search + confirmation gates | M1 | flow |

M1 and M2 are prerequisites for everything and are each ~20 lines. They ship
first because the rest of this spec adds work to a frame whose cost is
currently mismeasured.

---

## 3. Design

### M1 — frame memo

`tableData()` (`model.go:730`) has no memo and calls `src.Rows(kind, ns)` on
every invocation. Per frame in table mode it is called **4–6 times**:
`view.go:695` (search box), `view.go:750` (body), `view.go:976` via
`curName()` → `curRow()` → `tableData()` *twice* (`model.go:833`, `:841`),
and `view.go:1023` via `rowStatus` twice more. `BenchmarkRowsPods` is 488µs /
8018 allocs for 2000 pods (`docs/performance.md:52`).

Add `rowsMemo`, cleared at the top of both `Update` and `View` beside
`kindsMemo` — the pattern already documented at `docs/performance.md:125-130`
("never staler than one frame"). Key it on `(kind, namespace, search)`.

Same fix applies to `paletteHits()`, called once per frame from
`palette_view.go:20` and **twice per click** (`model.go:2747`, `:2750`).

### M2 — perf guards that measure what they claim

`TestKeypressLatency` (`model_test.go:97-121`) drives `key("j")` and
`key("k")`. In `focusMain`, `j` is unbound and `k` calls `openPrompt("k")`
(`model.go:1666`). From iteration 2 every keystroke is **typed into the
prompt text field**, so the measured frame is a zoomed prompt with a ~400-char
buffer, not table navigation. `BenchmarkKeypressFrame`
(`bench_test.go:36-47`) has the identical defect.

- Use `key("down")` / `key("up")`, which reach `m.move()` (`model.go:1660`).
- Tighten `model_test.go:87` from `gotRows >= nKinds` (30, ~7× slack over the
  actual 4) to `gotRows > 1`. After M1 the true answer is 1.

This is a prerequisite, not a cleanup: without it, M6 and M8 can add row
builds per frame and CI stays green.

### M3 — layout budget

At 80×24 chrome costs **8 of 24 rows (33%)** before a pod is drawn:
`headerH: 4` (`model.go:919`) where row 2 is blank and row 4 is a rule
(`view.go:188-192`), plus a 3-row prompt and a 1-row status bar. Side panes
take **38 of 80 columns (47%)** — `leftW`/`rightW` are constants
(`model.go:929-931`) that only collapse on `z`. The Actions pane at 160×48 is
11 content rows and 27 blank ones.

Neither header line is width-budgeted. `line0` clips mid-token (80-col frame
ends at `│  nodes`, and the clickable `ns ▾` / `theme ⟳` buttons are off
screen with their zones still marked — the mouse affordance dies silently).
Row 3 ends `42%    81`, a memory figure cut inside the number.

Three breakpoints, one function:

| Width | Header | Left pane | Right pane |
|---|---|---|---|
| ≥ 120 | 4 rows, gauges width 16, absolute figures shown | 22 | 24 |
| 96–119 | 2 rows (identity + gauges, no blank, no rule), gauges width 10, figures hidden | 18 | 20 |
| < 96 | 1 row (identity + gauges width 6) | 0 | 0 — actions move to the status bar |

`headerH` becomes a function of `m.w`; it is already read through `l`
everywhere (`view.go:193`, `palette_view.go:93`), so the change is contained.
Every header segment is assembled against a remaining-width budget and
**dropped whole, never clipped** — a zone is marked only if its segment was
drawn. Below 96 the `zoomed` code path already exercises `leftW = rightW = 0`.

### M4 — column policy

Three defects, all in `tryFit`/`fitCols` (`view.go:373-436`):

1. **Wrong shrink victim.** The loop shrinks the widest over-minimum column
   (`view.go:422-428`), which is always NAME. At 100 cols NAME is truncated
   to `api-gateway-7d9f4…` *while four columns are still displayed* — two pods
   differing only in their hash suffix become indistinguishable.
2. **Positional drop.** `keep = keep[:len(keep)-1]` (`view.go:383`) drops
   right-to-left with no notion of importance. Measured: AGE dies first at
   every width, and STATUS is truncated to `x Crash…` at 80.
3. **Byte width.** `tryFit` measures with `len(r[ci])` (`view.go:395`,
   `:401`) while cells are padded and cut by display width (`view.go:886`).
   Any non-ASCII cell — event messages, i18n namespaces — over-reserves and
   pushes real columns off the right edge. Use `lipgloss.Width`.

Fix: one lookup table beside the three that already key off the header string
(`view.go:410`, `view.go:767`, `trend.go:64`), giving each header a **weight**
and a **priority**.

- Shrink picks the highest `natural[i] / weight[i]`, not the highest
  `natural[i]`. NAME weight 3; STATUS, READY weight 2; the rest 1.
- Drop picks the lowest priority, not the rightmost. Identity column priority
  100 (never dropped); STATUS 90; AGE 80; READY 70; the rest 50.

Explicitly **not** done: replacing `Cols []string` with a `Column` struct.
That touches 30 kind literals in `internal/k8s/kinds.go`, every positional row
builder, `applyNamespace`'s prepend (`rows.go:62`), and all of `internal/mock`
— and `docs/plan.md:214-220` forbids rearranging shared files. Weight and
priority derive from the header string, as three existing lookups already do.

Explicitly **not** done: drag-to-resize. `tea.WithMouseCellMotion()`
(`main.go:118`) does report motion under a held button, so it is possible.
But the target is the 2-cell `gap` (`view.go:749`), and it collides with the
drag-to-select workflow that `ctrl+s` exists to enable
(`docs/keybindings.md:31-36`). Weighted shrink is ~6 lines, no state, no
gesture, and fixes the actual complaint for every user. If manual override is
still wanted after M4 ships, `ctrl+←`/`ctrl+→` adjust the focused column by
±2 cells into T23's `views.yaml`, clamped to `[min, avail/2]`, with `=` to
reset — keyboard only.

**Honest columns.** `-` currently means *unknown*, *unset*, *defaulted*, *not
applicable* and *pending* — five states, one glyph, all rendered `subtle`
(`view.go:452`). Replace with a four-word vocabulary: `<none>` (deliberately
absent), `n/a` (not applicable to this object), `pending` (expected, not yet,
graded `warn` so `cellLevel` glyphs it), and a real value where one is known.
Specific corrections:

| Column | Where | Today | Should be |
|---|---|---|---|
| CPU/MEM (pods) | `rows.go:355` | `-` when metrics-server is absent, graded benign | hide the column when metrics-server is unreachable |
| MIN (hpa) | `rows.go:900` | `-` where the Kubernetes default is 1 | `1` |
| IMAGE (deploy) | `rows.go:409` | first container only, header unqualified | `IMAGE(1)` or append `+2` |
| READY (pods) | `rows.go:347` | counts `Spec.Containers` only, so `Init:0/2` shows `0/1` — and disagrees with `podStatus` (`rows.go:373`) | align the two |
| ADDRESS (ingress) | `rows.go:562` | `-` for both "just created" and "3-day outage" | `pending`, graded `warn` past a threshold |
| CAPACITY (pvc/pv) | `rows.go:598`, `:1233` | `-` means unbound (PVC) or a spec bug (PV) | distinguish; PVC unbound reads `Status.Phase`, grades `warn` |
| NAMESPACE (cluster-scoped CR) | `rows.go:830` | `-` | `<cluster>` |

### M5 — sort

Implements existing card **T07** (`docs/plan.md:491-524`). T07 owns the state
model (`sortCol`, `sortDesc`, per-kind), the comparator (`domain.CompareCell`)
and the header arrow. Decisions it leaves open:

- **Copy before sorting.** On the unfiltered path `tableData` returns the
  backend's own slice; sorting in place mutates informer-derived state that
  `Rows()` may reuse.
- **Precompute the key.** Parsing `"5d2h"` inside the less function is
  O(n log n) parses. Build `[]sortKey` in one pass, sort indices.
- **Type per column, not per cell.** Sniff the first ~20 non-empty cells; all
  one type → use that comparator; mixed → `NaturalLess`. Per-cell sniffing
  makes `-` and `250m` incomparable and the order non-deterministic.
- **Sentinels sort last in both directions.** `-`, `""`, `<none>`, `n/a`.
  A descending CPU sort that floats every unmetricked pod to the top is
  useless.
- **Severity is a sort mode.** `cellLevel` (`view.go:469-496`) already grades
  cells; expose `sort: severity` so "worst first" works on builtin kinds, as
  lens kinds already get free (`lensrows.go:197-239`).

Keys: click header cycles asc → desc → default (T07). `shift+<n>` as written
in T07 is unreliable — terminals send `!@#$%^&*(` for shift-digit, and `1`–`9`
are claimed by T12's saved views. Use `<` / `>` to move the sort column and
`S` to flip direction; `s` is Shell and `ctrl+s` is mouse capture
(`docs/keybindings.md:16`, `:111`).

Header zones: new id namespace `hdr:%d`, index into `keep` — bounded ids, as
`zones.go:49-52` requires. The `▲`/`▼` must be reserved through the existing
`extra[]` mechanism (`view.go:789`), never by widening the header: `tryFit`
floors width at `len(cols[ci])` (`view.go:403`), so a 2-cell indicator would
promote `RESTARTS` from 8 to 10 and the sort indicator itself would knock a
column off screen.

### M6 — row groups, and grouping by owner

This is where the spec departs from the literal instruction. **Stated
decision: "tree by default for owned kinds."** A true tree in the main list
would break five things that currently work, and the reasons are structural:

1. **Sort stops meaning anything.** A tree orders siblings; "sort by RESTARTS
   descending" across a tree is undefined. Today it is one click (after M5).
2. **Filtering forks into two wrong answers.** Show orphan matches and the
   tree is a lie; show ancestors of matches and rows appear that do not match.
   The sidebar dodges this by declaring folding irrelevant during search
   (`model.go:697`) — a tree cannot, because ancestors are structurally
   required, not merely hidden.
3. **Row numbers stop being addressable.** Row 12 is a different object
   before and after a fold. `rowNumBase` (`view.go:1377`) shows the project
   already treats numbering as load-bearing.
4. **500-pod namespaces get worse.** Flat: one scroll, one `f`. As a tree:
   ~40 Deployments + ~90 ReplicaSets + 500 pods = 630 lines, and finding one
   pod now requires knowing its Deployment. `treeMaxNodes = 120`
   (`tree.go:43`) exists precisely because unbounded fan-out is the failure
   mode.
5. **Selection doubles in arity.** `curRow()` (`model.go:820`) returns
   `[]string`. A header is not an object, so `curName()`, the Actions pane,
   `D`, and `enter` each need a "this row is not a thing" branch — five new
   ways to fire an action against the wrong object.

**What ships instead, and why it satisfies the intent.** Pods are grouped by
owner **by default**, as one level of collapsible headers over a row slice
that stays flat, globally sorted, and flatly indexed. Group headers are a
rendering concern. The operator gets hierarchy on open; sort, filter, row
numbers and selection keep their current contracts exactly. The genuine
multi-hop tree stays where it already works: the `X` panel (`tree.go:62`),
which needs built-in ownerRef edges — that is card **T14**, and M6 does not
duplicate it.

If a literal nested tree in the main list is still wanted after seeing M6 in
a frame, it is a separate card and this spec's §8 lists what it costs.

**Owner without extra watches.** The objection to owner grouping is that
resolving `pod → ReplicaSet → Deployment` needs the RS and Deployment
informers, which viewing Pods deliberately does not start
(`docs/performance.md:31`, `TestOpeningOneKindWatchesOnlyThatKind`). It does
not: `metadata.ownerReferences` is **on the pod object already**. Group by
`ownerReferences[0].name` — zero extra requests, zero extra watches.

The Deployment name is then *derived*, not fetched: a ReplicaSet named
`web-frontend-6b8c7d9f5` matching `^(.+)-[a-z0-9]{6,10}$` yields
`web-frontend`. Render it as `web-frontend · rs 6b8c7d9f5`. When the pattern
does not match, print the owner name verbatim. **Never print a Deployment
name that was not derived from a matched pattern** — a confident wrong owner
is worse than an RS hash.

This requires a new `OWNER` column on the pod row builder (`rows.go`), hidden
by default via M4's priority table but present in the row slice so grouping
reads a materialised cell and never does I/O.

**Group keys.** One at a time, no nesting, each sourced from a column already
in the row:

| Key | Source | Enabled when | Default for |
|---|---|---|---|
| `owner` | `OWNER` column | the kind's rows carry ownerRefs | **pods** |
| `node` | `NODE` column | the kind has a NODE column | — |
| `namespace` | `NAMESPACE` column | `m.namespace == AllNamespaces` | — |
| `status` | the graded column | that column exists | — |
| `object` | `OBJECT` column | `kind == "events"` | **events** |
| `none` | — | always | every other kind |

`label:<k>` is deliberately absent: labels are not in the row set, and
shipping a key that silently returns one group called `—` is worse than not
offering it. It arrives when a label column does.

**Behaviour, mirroring the sidebar exactly** — the sidebar's rules are already
tested (`groups_test.go`) and already explained in `docs/ui.md:83-85`:

- Collapse state: `map[groupKey]map[value]bool`, all groups **open** by
  default. Not persisted — a folded row-group costs nothing (unlike a folded
  sidebar group, which suppresses badge requests), so restoring a session with
  half the pods hidden is a surprise.
- `space` folds the group under the cursor, and stays a search character while
  searching (`model.go:1481`). No `left` binding here — `←` already focuses
  the resource list.
- **Search ignores collapse entirely.** While `m.rowSearch != ""` every match
  renders, headers appear above groups that have matches, and groups with no
  matches disappear rather than showing an empty header. Verbatim from
  `model.go:697-699`: *a match hidden behind a fold would make the filter look
  broken.* `:filter` inherits this, since both write `m.rowSearch`.
- **Sorting and grouping are exclusive.** Sorting by any column drops to flat
  and the panel title says so (`[ sorted by RESTARTS ▼ · flat ]`). This is
  what keeps M5 honest, and it is one branch rather than five.
- Headers are unnumbered (blank gutter). Object rows keep **one continuous
  1..N sequence across the whole table**, taken from the row's index in the
  ungrouped slice, not the render index — so collapsing a group does not
  renumber the rows below it. This is the one real change to `view.go:870`.
- Headers are never selectable; `↑`/`↓` skip them. Collapsing the group
  holding the selection moves the selection to that group's first row and
  marks the header, as `groups_test.go:217` requires of the sidebar.
  `curRow()`, `curName()` and the Actions pane are untouched.

**Auto-flatten**, silently, when: the group column is missing for this kind;
fewer than 2 distinct values; more than 40 groups; or more than 2000 rows. All
four take the existing `tableBody` path.

**Cost.** One O(N) pass over rows already in hand, one string compare per row,
one `[]groupSpan{value, firstRowIdx, count}` allocation. ~500 compares against
the 488µs the row build already costs. Memoised beside `kindsMemo`, cleared at
the top of `Update` and `View`. Collapse state is *not* in the memo — it is
read at render time, so folding is a pure repaint, which is what makes `space`
feel instant.

**Config:** `group: "pods=owner,events=object"` — flat, string-valued, one
line, matching `docs/config.md:26`. Absent = the defaults above.

### M7 — the view engine

The centre pane becomes a mode switch over one contract:

```
rows   [][]string      // unchanged, flat, globally ordered
meta   []rowMeta       // parallel: groupValue, hidden, ordinal
spans  []groupSpan     // value, firstRowIdx, count
```

Modes: `table` (today), `grouped` (M6), `text` (describe/YAML/help/tree —
today), `chart` (M8). Each mode is a function from that contract plus a
`layout` to a `Block`. `zoom`, the scroll model and the zone namespaces are
shared.

The point of the abstraction is that the next view — a port-forward manager,
a pulse dashboard — is a new function and a case, not another branch inside
`tableBody`. It is the *only* abstraction this spec adds, and it is added
because four views already want it.

**Also fixed here, both cheap:** the table has no position indicator while the
text view has one (`38/66  57%`); add `n/m` to the panel title. And
`docs/keybindings.md:10` documents `←`/`h` as "focus resource list" — neither
is bound in `focusMain` (`model.go:1606-1702`); `h` falls through to the
Actions loop and does nothing. Bind them or correct the doc.

### M8 — metric history and charts

**Build, do not buy.** The stated dependency policy is pragmatic — allow a
charting library if it beats hand-rolling. Measured against this codebase, it
does not, and the reasons are specific rather than principled:

- `bubbles/progress` is **already in `go.mod`** (v0.21.0) and is still the
  wrong choice: it renders a gradient via a `lipgloss.Style` per segment,
  which is exactly the per-cell `Style.Render` cost that `paint`
  (`block.go:35`) exists to avoid and that accounts for 43% of the frame
  (`docs/performance.md:106-120`). It has no threshold, no grade, no ASCII
  mode.
- `ntcharts` is good and Bubble Tea-native, but brings its own canvas,
  viewport and zone handling — a second rendering model beside `block.go` and
  `zones.go`, the latter of which exists *because* bubblezone was removed on
  measurement (4.4 MB of the 4.8 MB allocated per frame, `zones.go:12-19`).
- `asciigraph` is ~700 LOC, emits a pre-escaped string `lipgloss.Width` must
  re-walk, does no braille, and knows nothing about `theme.Theme`.

The whole vocabulary is four ladders of runes and ~135 lines of Go. btop, btm
and gotop are the right *references*; none is a library you can link.

**Constraint on the palette:** `theme.Theme` has exactly 12 tokens
(`theme.go:16-30`) and custom themes are `UnmarshalStrict` with every field
required (`theme.go:109`, `:126-137`) — **adding a token breaks every user's
existing theme YAML.** Everything below lives inside those 12.

**(a) Ratio bars.** Replace the `▰`/`▱` pair (`view.go:68-84`) with
block-eighths on a dotted trough. Fill `█` U+2588, partials `▏▎▍▌▋▊▉`
U+258F–2589, trough `·` in `Border`, threshold ticks `┊` at 60% and 85% in
`Subtle`, and a leading grade mark `·` / `!` / `×`.

This fixes a real bug: `filled := pct * width / 100` truncates with no floor
(`view.go:76`), so at width 16 **any pct in 1..6 renders as zero filled
cells** — a node at 6% is pixel-identical to a node at 0%. Negative `pct` is
unguarded and would panic in `strings.Repeat`. Hoist the hardcoded 60/85
thresholds (`view.go:69-75`) to named constants; four call sites want them.
Quota and PDB reuse the same function.

**(b) Inline sparklines.** `▁▂▃▄▅▆▇█` U+2581–2588, 8 samples, oldest→newest so
the newest glyph sits beside the number it explains. Scaled **per row against
that row's own window max** — a 5m sidecar and a 4-core gateway each need to
show *their* shape. Behind a `:set spark` toggle, default off: the CPU column
at 80 cols cannot afford 8 more cells.

**(c) Chart panel.** Braille (`U+2800` + bitmask) gives 2×4 subcells, so
60×8 plots 120 samples at 32 vertical steps. One selected object, not the
table. CPU as the braille curve in `Accent`, MEM as a dotted underlay in
`Accent2` — **stroke, not hue**, carries the difference, plus end-of-line
value labels. Minimum panel 24×6; below that, draw the (a) bar and say nothing
else.

**(d) State strips.** One cell per pod, worst-first so failures cluster left:
`▪` Running (`Ok`), `▫` Pending (`Warn`), `▮` CrashLoop/Failed (`Err`), `▯`
Terminating (`Subtle`), `?` Unknown. Four distinct shapes — filled/hollow ×
short/tall — readable in monochrome.

**Accessibility, non-negotiable.** Length or height is always the primary
encoding; color only grades. Red/green is the worst pair for deuteranopia and
is currently the *only* signal in the gauge (the trend arrow's shape rescues
the arrow; the gauge has nothing). Guard it the way `glyphs_test.go:99`
guards the table: **assert that two different grades never produce the same
rune sequence.**

**ASCII fallback**, chosen once at startup from `K10S_ASCII=1`, a `$LANG`
without `UTF-8`, or `ascii: true` in config — never per call. Truecolor
degradation is already free: `paint`'s cache keys on
`lipgloss.ColorProfile()` (`block.go:35`, `:56`). What is *not* free is that
`Ok`/`Warn`/`Err` collapse to near-identical ANSI16 slots — which is exactly
why every element above has a shape channel.

**History lives off the render path.** The current trend map is written from
`View` (`arrowFor` inside `tableBody`'s row loop, `view.go:806-823`) and that
pattern must not be copied, for three reasons:

1. `View` runs on every keystroke, not per tick. `m.anim` only increments in
   `Update` (`model.go:975`), so a ring fed from `View` appends duplicates
   during a keystroke burst and nothing while idle — a time axis that is not
   time.
2. `View` walks only visible rows (`view.go:850`). Scroll past a pod and it
   stops being sampled; scroll back and its sparkline has a hole it cannot
   know about.
3. The real cadence is the **15s metrics ticker** (`store.go:439`), the only
   rate at which new information exists. Sampling faster manufactures
   resolution.

Shape: `ring[N]int32` + `head` + `n`, two rings per object (cpuMilli, memMiB).
`int32`, not `uint16` — a 64-core pod is 64000 milli and MiB overflows 16 bits
at 64 GiB. **N=16** for the inline spark (16 × 15s = 4 minutes, the honest
window for a 15s sampler); **N=120** for the chart, kept for the *selected
object only* (30 min). At 5000 pods: 2 × 16 × 4 B + map overhead ≈ 210 B/pod ≈
**1.05 MB**. The 120-sample variant would be 5.3 MB, which is why it is
selection-scoped.

Fed from a `metricsTickMsg` in `Update`, gated on a monotonic
`Store.metricsGen` so the ring appends exactly once per *new snapshot*, never
on a repaint. Swept on the same tick: drop keys absent from the current row
set — which also fixes the existing unbounded `m.trends` map, cleared today
only by `resetTrends` (`trend.go:90-93`).

### M9 — flows, actions, safety

**Action search.** The palette (`palette.go:54-92`) finds kinds and objects,
never verbs. Extend `paletteHits` to match `Actions`, lens specs and plugins,
with the applicable kind on the `sub` line, firing through `fireAction`. This
reuses the palette's overlay, key handling, zones and mouse path wholesale,
and gives `R`, `X` and `ctrl+y` their first discoverable home — today they
appear in no pane and no hint string (`view.go:1172`).

Chosen over a which-key transient menu (a new timer message class in an
already-1400-line `Update`) and over rotating status-bar hints (cheapest,
least information).

**Confirmation gates.** The typed gate exists and lens packs already use it
(`lens.go:139-140`). Two core actions should adopt it:

| Action | Today | Should be |
|---|---|---|
| `D` Delete (`model.go:2181`) | `danger: true`, plain Enter — and `enter` is also the universal "open" key, so `D`,`enter` deletes | `typed: name` |
| `u` Drain (`model.go:2229`) | `danger: true`, plain Enter | `typed: name` |
| `r` Restart (`model.go:2170`) | neither `danger` nor `typed`; its own message claims "zero downtime with 2+ replicas" without checking | mark `danger` when replicas < 2 |
| `e` Edit → Apply (`model.go:1189-1210`) | applies **unconditionally** on editor exit — quitting `vi` with `:q`, a truncated file from a crashed editor, or an empty file all reach `src.Apply` | compare against the fetched bytes; skip silently when identical, diff + confirm when not |
| `o` Cordon (`model.go:2219`) | no gate | correct as-is — it is a toggle and the label flips |

There is no undo, and given `src.Delete`/`src.Drain` that is the right call:
the answer is a stronger gate, not a rollback. One invariant to preserve:
`confirm.armed()` is enforced identically on the keyboard (`model.go:1318`)
and the mouse OK button (`model.go:2729`), with a comment saying why.

**Deliberately out of scope: multi-select.** There is no model for it —
`m.mark` (`model.go:2927`) is the mouse-zone marker, and every action path is
singular by construction (`model.go:821-865`, `:2136-2246`). It is card T08.
Two constraints for whoever takes it: marks must key on
`(kind, namespace, name)`, never on a row index (`tableData` returns a fresh
slice on every call and `applyNamespace` resets `rowIdx`, `model.go:775`); and
`runAction` (`model.go:1259`) takes one `okToast` and one `error`, so it
cannot express partial failure.

---

## 4. Commands

Unchanged: `just build`, `just dev`, `just dev-demo`, `just test`,
`just test-one <pattern>`, `just test-race`, `just check`,
`just shot W H <keys>`.

`just check` must pass before any card is done. `just test-perf` runs the
guards alone. Every UI change is verified against a real frame at **160×48,
100×30 and 80×24**, per `CLAUDE.md` — never by reading view code.

New keys introduced by this spec, checked against `docs/keybindings.md:7-28`
and `:108-121`:

| Key | Module | Action |
|---|---|---|
| `space` (main pane, table, not searching) | M6 | fold/unfold the group under the cursor |
| click a `▾`/`▸` row-group header | M6 | toggle |
| click a column header | M5 | cycle asc → desc → default |
| `<` / `>` | M5 | move the sort column |
| `S` | M5 | flip sort direction |
| `:group <key>` | M6 | set group-by; bare `:group` clears |
| `:set spark` | M8 | toggle the sparkline column |

No new letter key is consumed beyond `S`: `g`/`G` are first/last, `f` is find,
`s` is Shell, `ctrl+s` is mouse capture, `X`/`R` are the graph views.

---

## 5. Project structure

New files, all beside their tests:

```
internal/ui/
  viewmode.go      viewmode_test.go    M7 — the mode switch
  rowgroups.go     rowgroups_test.go   M6 — spans, collapse, auto-flatten
  columns.go       columns_test.go     M4 — weight, priority, display width
  sortrows.go      sortrows_test.go    M5 — sort keys over tableData
  gauge.go         gauge_test.go       M8 — bars, sparks, strips, glyph sets
  chart.go         chart_test.go       M8 — braille plotting
  history.go       history_test.go     M8 — rings, feed, sweep
internal/domain/
  compare.go       compare_test.go     M5 — CompareCell (T07 names it)
```

Modified: `view.go` (layout budget, column policy, group headers, row
numbering), `model.go` (memos, group state, confirm gates), `trend.go`
(sweep), `palette.go` (action hits), `internal/k8s/rows.go` (OWNER column,
honest-column fixes), `docs/ui.md`, `docs/config.md`, `docs/keybindings.md`,
`docs/performance.md`.

---

## 6. Testing strategy

Test-driven: the failing test lands in the same commit as the change, beside
its source, named `Test<WhatIsTrue>`, driven through the model with the mock
backend — the shape `groups_test.go` and `tree_test.go` already use.

**Regression gate for the whole spec.**
`TestGroupByNoneRendersTodaysFrameExactly` — byte-compare `just shot 140 44`
with grouping off. If this fails, something changed that should not have.

**Golden frames.** There is no `testdata/` harness today; frames are verified
by eye. Add the thinnest one: build from `mock.New` at fixed sizes, render,
compare against `internal/ui/testdata/*.golden`, `-update` to regenerate.
`cmd/shot/main.go:58` already pins `termenv.TrueColor`, so bytes are
deterministic. Required frames: `80x24`, `80x24-ascii` (no truecolor),
`100x30`, `160x48`, plus one per grade (35% / 70% / 92%) so threshold ticks
and grade marks land in the diff.

**Per module**, the tests that would catch the specific defect:

- **M1** `TestViewBuildsRowsOnce` — one `View()`, exactly one `Rows()`.
- **M2** `TestKeypressLatencyMeasuresNavigation` — assert focus is still
  `focusMain` after the drive loop (today it is `focusPrompt` from iteration
  2).
- **M3** `TestHeaderNeverClipsMidToken` at 80/96/120;
  `TestZoneMarkedOnlyIfDrawn` — a zone whose segment was dropped must not be
  scannable.
- **M4** `TestNameSurvivesUntilColumnsAreExhausted` over 80/100/140/160;
  `TestWidthIsMeasuredInCellsNotBytes` with a CJK cell;
  `TestLowestPriorityColumnDropsFirst`.
- **M5** `TestSortDoesNotMutateBackendRows` (snapshot `Rows()`, sort, compare
  byte-identical); `TestSortKeyParsedOncePerRow` (counting comparator: assert
  n, not n log n); `TestSentinelsSortLastBothDirections`;
  `TestEventsDefaultToNewestFirst`; `TestSortIndicatorKeepsRowWidth` at every
  width.
- **M6** mirrors of the sidebar's tested rules, one each:
  `TestSearchShowsMatchesInCollapsedRowGroups` (`groups_test.go:125`),
  `TestCollapsedGroupHoldingTheSelectionIsMarked` (`:217`),
  `TestArrowsSkipRowGroupHeaders` (`:176`),
  `TestSpaceIsTypeableIntoTheRowSearchBox` (`:236`). Plus
  `TestRowNumbersAreContinuousAcrossGroups`,
  `TestCollapsingAGroupDoesNotRenumberRowsBelowIt`,
  `TestSortingDropsToFlat`, `TestOwnerGroupingStartsNoInformers` (the
  analogue of `TestRowCountStartsNoInformers`, and the test that makes "no
  extra watches" enforceable rather than a note in a doc),
  `TestDerivedDeploymentNameIsOnlyShownWhenThePatternMatches`.
- **M8** `TestGaugeFillRounding` — table over `(pct, width) → filled`,
  asserting `pct=1, width=16 → 1` (**today 0**), `pct=99 → 15+⅞` (99% and
  100% must be distinguishable), `pct=-1 → 0` (no panic), `width=0 → ""`.
  `TestGaugeWidthIsExact` for every pct 0..100 at widths {6,10,16} in both
  glyph sets. `TestGlyphSetsAreSingleWidthAndDistinct` — extends
  `glyphs_test.go:99`; no two grades share a rune; ASCII mode is all `< 0x7f`.
  `TestHistoryIsNotFedByView` — render 20 frames with no tick, assert the
  sample count is unchanged. `TestHistorySweepDropsVanishedPods`.
- **M9** `TestDeleteRequiresTypedName`; `TestEditWithNoChangesDoesNotApply`;
  `TestPaletteFindsActionsByName`.

**Frame budget, and the gate.** ≤ **1 ms** and ≤ **512 KiB** per `View()` at
140×44 with 2000 rows; ≤ **4 ms** for keypress+frame.
`docs/performance.md:145` records the current 140×44 demo frame at 0.45 ms /
0.38 MB, so 1 ms is ~2× headroom — the existing 8 ms threshold is 18× and is
not a budget. Gate CI on **allocs/op**, which is stable across machines in a
way nanoseconds are not. `BenchmarkView` and `BenchmarkKeypressFrame` with
`-benchmem` against a mock seeded to 2000 rows.

---

## 7. Boundaries

**Always**

- Verify against a real frame at 160×48, 100×30 and 80×24 before calling a
  card done.
- Keep `View` free of I/O and of row building. Anything per-tick goes on a
  background goroutine or a memo, per `docs/performance.md:24`.
- Carry severity in a glyph as well as a color, always.
- Put the test beside the source, in the same commit.
- Run `just check`.

**Ask first**

- Anything that changes `Cols []string` in `internal/k8s/kinds.go`, or any
  other shared file `docs/plan.md:214-220` protects.
- Adding a `theme.Theme` token — it breaks every user's theme YAML
  (`theme.go:126-137`).
- Any new module in `go.mod`.
- Changing a documented keybinding.

**Never**

- Re-add `bubblezone` — removed on measurement (4.4 MB of 4.8 MB per frame,
  `zones.go:12-19`).
- Swap the hand-rolled table for `bubbles/table` — it has no per-column
  minimums, no severity reserve, no zone markers and no `rowScroll` contract,
  so it loses `fitCols`, the click targets (`view.go:918`) and the perf guard.
- Render describe/YAML through `glamour` — `kubectl describe` is not Markdown
  and glamour would mangle indentation-significant text, for goldmark + chroma.
- Add spring physics or interpolated row movement — it burns CPU to convey
  nothing and destroys `just shot`'s determinism, the one thing that makes UI
  changes verifiable with no TTY.
- Add drag-to-resize splits — it collides with the drag-to-select workflow
  `ctrl+s` exists to enable.
- Migrate to bubbletea/lipgloss v2 as part of this work.
- Print a derived Deployment name that was not matched from the owner pattern.
- Store per-kind maps in `config.yaml` — its parser (`config.go:147-225`) is a
  flat hand-rolled subset that cannot express a list-of-map. Per-kind state
  goes in `views.yaml` (card T12's file).

---

## 8. Rejected, with reasons

| Rejected | Reason |
|---|---|
| Nested tree as the main-list **default** | Breaks sort, filter, row numbering, selection arity, and 500-pod namespaces (§3 M6). One-level owner grouping gives the same read with none of it. Kept as an **opt-in** mode behind `T` — card **T46**, deliberately sequenced after T42 so the cheap version is seen on a real frame first. T46 also needs a ReplicaSet informer, which it may start only on the keypress, never on opening the kind. |
| Owner grouping via an informer walk | Starts RS + Deployment watches the user never asked for (`docs/performance.md:31`). `ownerReferences` is already on the pod. |
| Sorting in the backend | `Rows()` and `RowCount()` share cached state; a UI preference behind the informer cache. Already ruled out by T07. |
| A `Column` struct replacing `Cols []string` | 30 kind literals, every positional row builder, all of `internal/mock`, and a shared file the plan protects. Weight and priority derive from the header, as three existing lookups do. |
| `ntcharts` / `asciigraph` / `bubbles/progress` | A second canvas model beside `block.go`/`zones.go`; a pre-escaped string `lipgloss.Width` must re-walk; a per-segment `lipgloss.Style` that is 43% of the frame. ~135 lines of Go instead. |
| Per-cell type sniffing in the comparator | O(n log n) parses and non-deterministic order for mixed columns. |
| Widening the header to fit `▲`/`▼` | `tryFit` floors width at the header length, so the sort indicator would itself drop a column. Use `extra[]`. |
| Per-column `width:` config as the narrow-terminal fix | Pushes an auto-layout bug onto the user. The layout is wrong; fix the layout. |
| Multi-select in this spec | Card T08. The flow gaps in M9 are cheaper wins and it needs a mark model this spec does not build. |

---

## 9. Build order

1. **M1, M2** — memo and honest guards. Nothing else is measurable until these
   land.
2. **M3** — layout budget. Independently shippable; fixes the 80×24 clipping.
3. **M4** — column policy. Fixes NAME truncation and the AGE drop.
4. **M5** — sort (T07). Unblocks T12 and T23.
5. **M6** — row groups, owner grouping on by default for pods.
6. **M7** — view engine. Extracted once M6 proves the second mode.
7. **M8** — history, then bars, then sparks, then the chart panel.
8. **M9** — action search, then the confirm gates.

M3, M4 and M9 have no dependency on each other and can land in any order.

---

## 10. Bugs found during the audit, not in this spec's scope

Each is a real defect, reproducible, and worth its own card:

1. **Header truncation at 80 cols** — `view.go:156-159` clamps the gap but
   never truncates `line0`; `view.go:169-175` builds an 82-cell row for a
   74-cell space. (Absorbed by M3 if M3 ships.)
2. **`m.trends` grows without bound** — `trend.go:97-108`, keyed per
   `kind/ns/name/col`, cleared only by `resetTrends` on backend switch.
   (Absorbed by M8's sweep.)
3. **`←` / `h` are documented but unbound in `focusMain`** —
   `docs/keybindings.md:10` vs `model.go:1606-1702`. `h` silently does nothing.
4. **`e` applies on editor exit unconditionally** — `model.go:1189-1210`.
   Quitting `vi` with `:q` writes to the cluster.
5. **No container picker** — `Store.Shell` (`k8s/exec.go:124-132`) calls
   `podContainer()` and never asks; `l` logs always read `Containers[0]`.
   Already card **T02** in `docs/plan.md`.
6. **`READY` disagrees with `STATUS` on init containers** — `rows.go:347` vs
   `rows.go:373`. A pod in `Init:0/2` shows `0/1`.
