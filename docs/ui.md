# UI

## Layout

```
TOP BANNER (no border, 4 rows incl. dashed rule)
┌ Resources ──[n/m]┐┌ <Resource> · <ns> ──────[ zoom ]┐┌ Actions ─┐
│ groups           ││   1 table row                   ││ [d] …    │
│ ▸ item         n ││ ▌ 2 selected row                ││ ───────  │
│                  ││   3 …          OR text view     ││ [D] Del  │
└──────────────────┘└─────────────────────────────────┘└──────────┘
┌ Command/Prompt ────────────────────[ CMD | AI · model ]┐
│ ❯ or ✦ input                                           │
└────────────────────────────────────────────────────────┘
status bar (toast · key hints)
```

Geometry (`layout()`): prompt 3 rows, status 1, middle gets the rest. The
banner and the side panes are budgeted by width, because at 80×24 the fixed
sizes spent a third of the rows and 47% of the columns on chrome:

| Width | Banner | Left | Right | Gauges |
|---|---|---|---|---|
| ≥ 120 | 4 rows | 22 | 24 | 16 wide, with `18.4/48 cores` |
| 96–119 | 2 rows (no blank, no rule) | 18 | 20 | 10 wide, no figures |
| < 96 | 1 row (identity and gauges share it) | 18 | 0 | 6 wide, no figures |

`z` still collapses both panes at any width. Below 72×22 the UI is replaced
by a "terminal too small" notice.

**The banner never clips mid-token.** Each part of the top line is a segment
with a priority (`hseg` / `fitSegs` in `view.go`), and a terminal too narrow
for all of them drops whole segments, lowest priority first — it does not cut
the line. Dropping order, least important first: version, product name, theme
button, namespace button, node readiness, demo tag, context name. The
namespace outranks the theme because it is also the mouse affordance; node
readiness outranks the namespace button because the namespace is already in
the main panel title while nothing else reports a node down. A dropped button
is not marked as a click target, so a mouse affordance never outlives its own
label.

Neither side pane spends rows on a permanent search box — see *Search boxes*
below.

## Row groups

Pods open grouped under their owner, Events under their object; every other
kind is flat. A header names the Deployment and the ReplicaSet it was derived
from (`▾ web-frontend · rs 6b8c7d9f5 … 3`) and carries the count of rows
beneath it.

The owner costs nothing to know: `ownerReferences` is already on the pod, and
the Deployment name is recovered from the ReplicaSet's pod-template-hash
suffix rather than looked up — so opening Pods still starts exactly one watch.
Where a name does not have that shape (a StatefulSet member, a bare pod) the
owner name is printed as-is; a Deployment name is never guessed.

This is **one level of headers over a flat row slice**, not a tree. Row
numbers stay continuous and count objects, so folding a group does not
renumber the rows below it; selection stays an index into the same flat slice,
so `curRow`, the Actions pane and every action are untouched; and headers are
never selectable. The nested tree is a separate, opt-in view (plan card T46);
the multi-hop relationship tree is already `X`.

| | |
|---|---|
| `space` | fold / unfold the group under the cursor |
| click a `▾`/`▸` header | either |
| `:group <key>` | `owner`, `node`, `namespace`, `status`, `object`; bare `:group` turns it off |

Rules, all shared with the sidebar's own folding:

- **A search ignores folding entirely.** Every match renders wherever it is —
  a match hidden behind a fold would make the filter look broken.
- A folded group holding the cursor keeps its marker, so "where am I" never
  becomes a guess. Arrow keys skip folded rows.
- **Sorting and grouping are exclusive.** Sorting states the whole table's
  order and grouping states its shape; honouring both would sort within
  groups, which answers neither question. A sort drops the table to flat.
- Grouping falls back to flat, silently, when the kind has no such column,
  when there are fewer than two distinct values, above 40 groups, or above
  2000 rows.

The group key persists per kind (`group:` in [config.md](config.md)). Which
groups are folded does **not** — unlike a folded sidebar group, which stops
that kind being counted, a folded row group saves nothing, so restoring a
session with half the pods hidden would be a surprise rather than a
preference.

## The owner tree (`t`)

Grouping is one level over a flat list. `t` opens the other thing: a real
Deployment → ReplicaSet → Pod tree where every node is an object.

```
▌deploy/api-gateway  2/2
 └─ rs/api-gateway-7d9f4c8b6d  2
    ├─ po/api-gateway-7d9f4c8b6d-2xk4p  + Running
    └─ po/api-gateway-7d9f4c8b6d-hv8qz  + Running
 deploy/billing-worker  ! 0/1
 └─ rs/billing-worker-6f8d9c5b7  0
    └─ po/billing-worker-6f8d9c5b7-qq91x  x CrashLoopBackOff
```

Each row carries its kind, because three kinds share one list and indentation
alone does not say which is which. **Actions follow the node under the cursor**
— `d` on a Deployment describes the Deployment, `l` on a pod reads its logs —
and the Actions pane lists what that node can do. The sidebar's kind still
drives the table underneath, so closing the tree puts you back where you were.

It is opt-in, and second on purpose. Most of what people want from "show me
the tree" is answered by grouping; the part that is not is the part that
costs. A tree needs the ReplicaSet and Deployment **objects**, which means
informers that opening Pods deliberately does not start — so `t` starts them,
on the keypress, and says so in the toast. Opening Pods still watches exactly
one kind.

- **A filter keeps the ancestors of every match**, dimmed and unselectable.
  Dropping them would lie about the shape; offering them as results would lie
  about what matched. That fork is the cost of a tree, and is why the flat
  list stays the default.
- **Row numbers are replaced by the branch drawing.** A number exists to
  address a row, and in a tree it stops referring to anything stable as soon
  as something above it is filtered.
- Pods with no Deployment above them — StatefulSet members, Job pods, bare
  pods — are listed at the root rather than hidden.
- Above 300 objects the tree **refuses to open** and says so. A tree that
  quietly stops is indistinguishable from a cluster that really is that small.

`X` is a different view: it walks the relationships a *lens pack* declares,
into the read-only text panel. `t` is built-in ownership, in the table, with a
cursor.

## Top banner (borderless)

Row 1: `⎈ k10s │ context │ ver │ nodes 2/3 ready … ns <name> ▾ │ theme <name> ⟳`

Identity on the left, two buttons on the right:

- the node counter turns warn-colored when any node is NotReady;
- **`ns <name> ▾`** opens the **Namespaces table in the main panel**, whose
  row `0` is `all` (real namespaces are then `1..N`, matching the sidebar
  count). `enter` there switches namespace and **returns to whichever kind
  you came from** — Services if you were looking at Services. `:ns` does the
  same;
- **`theme <name> ⟳`** opens the theme picker (the same one `/theme` shows).

Row 3: cluster totals — CPU and MEM gauges (average of per-node usage from
metrics-server). Per-node detail lives in Resources ▸ Nodes, not the banner.
Gauges color by threshold: ok < 60% ≤ warn < 85% ≤ err. A trend arrow
follows each percentage: red `▲` when usage just rose, green `▼` when it
fell. It stays for about half a minute of unchanged readings (`trendTicks`
in `trend.go`) and then clears. The same arrows sit at the end of the
`CPU`/`MEM` cells in the Pods table and `CPU%`/`MEM%` in Nodes, tracked per
row, so a pod that just spiked stands out without reading every number.

## Resources pane (left)

30 resource kinds grouped Workloads / Network / Config / Storage / RBAC /
Cluster / Custom Resources, each with a row count for the current namespace.
Selected row: `▸` + accent. `:po`, `:deploy`, `:pv` … jump straight to one —
see [commands.md](commands.md).

### Scrolling

The wheel scrolls this pane, but **scrolling never changes the selection** —
it is looking, not picking, so the main panel stays on the kind you chose.
That distinction is why the pane used to refuse the wheel outright: when
scrolling *was* selecting, brushing the wheel on the way to the table swapped
out the whole view.

`↑`/`↓` in the panel title say which way there is more. Moving the selection
with the arrow keys drags the window along just far enough to keep it
visible, its group header included — selection moves the scroll, never the
other way round.

### Folding groups

Thirty kinds do not fit a laptop-sized sidebar, so the groups people consult
occasionally — **Config, Storage and RBAC** — start folded, and the choice
persists (`collapsed` in [config.md](config.md)).

| | |
|---|---|
| `space` (list focused) | fold / unfold the group you are standing in |
| `left`                 | fold it |
| click a `▾`/`▸` header | either |

A folded header states how many kinds it hides, and keeps the `▸` marker if
the kind currently open is one of them — folding must never turn "where am
I" into a guess. Arrow keys walk only what is on screen and never open a
group you folded; arriving by `:sec`, `ctrl+p` or a search does unfold the
group it lands in, since the alternative is a table whose sidebar row is
invisible. **A search ignores folding entirely**: a match hidden behind a
fold would make the filter look broken.

Folding is not only tidiness. The sidebar is what tells the backend which
kinds to count, so a folded group stops sending badge-count requests
altogether — see [performance.md](performance.md).

**Counts.** The live backend only watches kinds you have opened, so counts
for the rest come from a cheap background sweep (one `limit=1` request per
kind, reading `remainingItemCount`). A kind whose count isn't known yet shows
no badge rather than a misleading `0`, and a count already on screen is kept
while a newly-opened kind syncs. See [performance.md](performance.md).

**Filtering.** `tab` to this pane and type: every printable key filters by
name / short name / group, case-insensitive; `↑↓` moves, `enter`/`→` returns
to the table, `esc` clears. `:search <term>` and `ctrl+p` do the same without
focusing. The active filter shows in the panel **title** (`Resources · po`)
and the match counter in the top-right tag (`3/30`), alongside the `↑`/`↓`
scroll hints. The selection snaps to the first match so the centre table
follows.

## Main pane (center)

Two modes:

- **Table** — a left gutter carries the selection marker `▌` and a dim
  1-based **row number**, sized to the largest number present. Remaining
  columns are auto-sized; when space runs out, widths shrink to per-column
  minimums (first column min 18, NAMESPACE min 9) and then whole columns drop
  from the right. Cell colors by status (`CrashLoopBackOff` err, `Pending`
  warn, `Running` ok, `x/y` mismatch warn); a NAMESPACE column renders in
  accent2. Selected row: selection bg, bold on the identity column (NAME, or
  OBJECT for Events — found by header name, not a fixed index, since that
  column shifts right under `:ns all`).
- **Text** — describe / YAML / help render in place with a `[ close ]` tag
  and a bottom scroll bar (`n/total  %  hints`).
- **Logs** — its own viewer; see below.

`[ zoom ]` (or `z`) hides both side panes; `[ restore ]` / `z` / `esc` undoes.
The choice is saved (`zoomed` in [config.md](config.md)), so a restart opens
zoomed if you left it that way.

### Sort order

Rows are sorted A→Z by name by default — informer caches return objects in
arbitrary order, so without this the table would reshuffle on every refresh.
Sorting is *natural*: `pod-2` comes before `pod-10`. Under `:ns all` rows
group by namespace first, then name.

**Events are the exception** and stay newest-first; sorting them by their
first column (TYPE) would bury the newest behind every `Normal` event.

### Shell

`s` opens an interactive shell **in the main panel**, not by handing the
whole terminal over: the resource list, header and status bar stay visible
while you are inside the pod. k10s runs a small terminal emulator (vt10x)
over the exec stream, feeds it the pod's output, and renders its screen into
the pane; the emulator is sized to the pane and resizes with it.

Every keystroke goes to the pod — `q`, `/`, `esc` and the action hotkeys all
belong to the program you are running. **`ctrl+]` detaches** (the telnet and
docker convention, chosen because nothing inside a shell wants it), as does
the `[ detach ]` button. When the shell exits on its own the panel returns
to the table.

Workloads resolve to one of their pods, the same way logs do. Backends
without a shell (the offline demo) say so in the status bar rather than
failing.

### Contexts

`:ctx` lists kubeconfig contexts in the main panel, numbered like a
table and marking the active one `current`. `enter` reconnects. Same idea as
the namespace chooser: full width beats a cramped popup for names this long.

The list always ends up holding one entry that is not a cluster:
**`k10s-demo`**, the built-in demo. It is labelled on its own row
(`k10s demo · sample data`) and explained in a legend under the list, because
a context list is exactly where someone decides what they are looking at, and
a name alone would not tell them. Picking any other context **leaves the
demo** — that is the only exit, and it is the same gesture as any other
switch. The list stays complete inside the demo: kubeconfig's contexts are
merged in from the startup read, so the way out is always on screen.

### Busy state

Any action that has to wait — describe, YAML, logs, top, AI, and the
mutating ones — takes over the main panel with a spinner naming what is
running. This covers `enter` and double-click as well, and matters most for
actions whose only result is a toast (port-forward, cordon), where silence
used to look like nothing had happened.

### Log viewer

Opened with `l`, `enter` or a double-click on anything that has logs.

- **Newest at the bottom**, and the view opens pinned there.
- **Line numbers count up from the bottom**: the newest line is always `1`,
  so a number keeps meaning the same thing as new lines arrive.
- **Opens at the bottom already**, with the newest 500 lines and no loading
  flash — the view is where you want it from the first frame.
- **Following**: new lines append at the bottom while pinned. Scrolling up
  pauses ("⏸ paused"), and returning to the bottom — or pressing `End` —
  resumes ("● following"). While paused the view holds still even as new
  lines arrive underneath.
- **Scroll-back is endless**: reaching the oldest loaded line pulls the next
  500 older ones, then the next 500, and so on. The Kubernetes log API has no backwards cursor, so
  "older" means re-reading with a larger tail; the status line says
  `↑ for older` until the beginning is reached, then `start of log`.
- **Lines wrap** instead of being cut off with an ellipsis — a truncated log
  line is often exactly the part you needed. Continuation rows have no
  number.
- **Level tokens are coloured** (`ERROR`/`FATAL` red, `WARN` amber, `INFO`
  green, `DEBUG` dim) and the leading timestamp is dimmed; the message itself
  stays in the normal foreground, because that is what you actually read.
- **Structured lines are parsed** (`t` toggles raw). A JSON record from
  zap/logr, or a logfmt line, is drawn as `time LEVEL message │ k=v k=v`: the
  message first, the context dimmed behind it, and the record's own timestamp
  dropped when the container already stamped the line. Anything that is not a
  structured record is shown exactly as it arrived — an unparseable line is
  still a line you need to read.
- **`f` filters, live** (`esc` clears): terms match, `-term` excludes, all
  case-insensitive, matched against the line as drawn. The status line counts
  `shown/loaded`; nothing is discarded, so clearing the filter brings it back.
- **`w` cycles the level floor**: all → `INFO` → `WARN` → `ERROR` → all. Lines
  with no level at all are never hidden — that is where the stack trace under
  the `ERROR` lives.

Kinds with no logs of their own fall back to **describe** rather than
reporting an error — there is nothing the user could do about "this kind has
no logs". Workloads (Deployment, ReplicaSet, StatefulSet, DaemonSet, Job)
resolve to one of their pods, preferring a running one.

### Loading state

While a kind's informer is still doing its first list, the pane shows a
centred spinner, an indeterminate bar and a one-line explanation — never
"no resources found", which would be a lie. The repaint tick runs faster
(150ms) while loading and backs off to 2s once synced.

### Demo

While the demo backend is on screen the header shows its context in the warn
colour plus a `DEMO  sample data · :ctx to leave` marker — on every frame, not
in a toast that scrolls away, because every number and gauge beside it is
sample data. `k10s demo` (shell), `/demo` (anywhere) and `:ctx` all reach it;
picking another context leaves it. See
[backends.md](backends.md#the-demo-as-a-context).

### No cluster

When the connection settles with nothing behind it — no kubeconfig, or a
context whose API server does not answer — the main panel becomes a
warn-bordered **No cluster** panel: the reason client-go gave, what k10s
reads (`$KUBECONFIG`, else `~/.kube/config`), three numbered steps with
links, and the two commands that check the connection from outside k10s.
`r` retries, `:ctx` picks another context, `/setup` opens the long guide
([cluster-setup.md](cluster-setup.md)).

Nothing on the frame is invented in that state: no rows, no sidebar badges,
`ver —` and `nodes —` in the header instead of a version and a `0/0 ready`,
and no CPU/MEM totals — those are computed against a per-node capacity
constant, so with no nodes they would be a capacity figure for a cluster
that is not there. The Actions pane says "nothing to act on" rather than
listing buttons that can only produce errors.

This is why the demo backend is opt-in (`k10s demo`). It used to be the
fallback here, which meant a machine with no cluster showed a full one.

A failed `:ctx` switch is a different case and stays on the cluster you have:
losing a working session because another cluster is down helps nobody.

## Search boxes

Neither pane keeps a search box on screen permanently:

- **Resources pane** — no box at all. It is type-to-filter, and the filter
  state lives in the title/tag.
- **Main pane** — the row-search box appears only while it's in use (focused,
  or holding a filter), so a table normally gets the full panel height. Open
  it with `f` — advertised as a `[ f to search ]` tag beside `[ zoom ]` — and
  the active filter also shows in the panel title.

## Actions pane (right)

Header shows the current target (`po/name`). The pane lists **only actions
that apply to the selected kind** — no dimmed rows. Risky actions (Drain,
Delete) sit below a rule and are drawn in the error color. With no cluster
it lists nothing at all — see [No cluster](#no-cluster).

Notable behavior:

- **Logs** (`l`) follows the stream live, like `kubectl logs -f`.
- **Shell** (`s`) opens a real interactive exec session **inside the main
  panel** — see below.
- **Port Forward** (`p`) starts a real forward on an ephemeral local port and
  toasts the address; pressing it again stops that forward.
- **Edit** (`e`) fetches live YAML into a temp file, hands the terminal to
  `$EDITOR`, and applies the result on exit.
- **Scale** (`c`) pre-fills the prompt with `:scale <current>` instead of
  guessing a number.
- Rows **light up under the pointer** and **flash** when clicked, so a click
  is acknowledged even when the action only produces a toast.
- **Cordon** (`o`, nodes only) toggles `spec.unschedulable`; the label flips
  between "Cordon" and "Uncordon" to match the node's state, and the STATUS
  cell gains a warn-colored `,SchedulingDisabled` suffix — the same
  convention `kubectl get nodes` uses.
- **Drain** (`u`, nodes only) cordons, then evicts every non-DaemonSet,
  non-mirror pod via the eviction API, reporting any that refused.

## Prompt

Bordered, mode tag on the right (`[ CMD ]` / `[ AI · model ]`, clickable),
and a `[ grow ]` / `[ shrink ]` button beside it.

Normally one input row. `ctrl+z` — or simply typing something that is not a
`/` or `:` command — grows it to half the screen and wraps the value across
the rows, because shell lines and AI prompts get long and a sideways-
scrolling one-liner hides most of what you typed. `esc` shrinks, `esc` again
leaves.

In **CMD** mode a line that is not a `/` or `:` command **runs in your
shell**, and its output opens as a text view titled with the command
(`$ date`). It runs off the event loop with a 30-second cap and no terminal
attached, so a hung command costs a spinner and then an error rather than a
frozen UI — and an interactive one (`vim`, `kubectl exec -it`) simply times
out; `s` on a pod is the real shell. See [commands.md](commands.md).

## Overlays

- **Confirm** (restart, delete, drain): centered, danger variant gets
  err-colored border/title; Enter·Confirm / Esc·Cancel buttons are clickable.
- **Search palette** (`ctrl+p`): one box over kinds and objects, each hit
  labelled with where it lives; the footer names how many kinds are matched
  by name only.
(Contexts and namespaces are not overlays — both list in the main panel.)
- **Theme picker** (`/theme`): each row carries a swatch of that theme's own
  colors, and moving the cursor **applies the theme immediately** so you see
  it before committing. `esc` restores whatever was active before.
- **Settings** (`/settings`): one dialog with the command
  names and the AI provider block (radio, Base URL, Model, masked API Key).
  `↑↓` moves, `enter` ticks a name or starts editing a field, clicking a row
  does the same, `tab` reaches Save. Text fields show a blinking caret.

  The built-in command names are **stated, not chosen**: `kubectl`, `k8s`
  and `k` always work at the prompt, so there is nothing to tick and no way
  to end up with none enabled. One row adds a name of your own, which then
  becomes the one shown in hints; clearing it falls back to `kubectl`.
- **Command popup**: appears while the input starts with `/` (do something)
  or `:` (narrow what's on screen) — each prefix lists only its own set.
  `↑↓` moves the highlight, **`enter` runs the highlighted command outright**,
  `tab` completes instead, rows are clickable.

The pickers share a **Save** button reachable with `tab`.

## Focus model

`tab` cycles **Resources → Main → command box** in layout order, and
`shift+tab` walks it backwards. The prompt can also be entered with `/`, `:`
or a click, and left with esc.

The **Actions pane is not in the cycle** — every action already has a hotkey
and a clickable row, so a tab stop there would be a keystroke that leads
nowhere. Clicking a resource row or an action acts on it without moving
focus.

Clicking blank space in the centre pane focuses it. **While a popup is open
the wheel belongs to the popup** — letting it through meant scrolling a
picker quietly moved the table behind it. The side panes respond to clicks
(pick a kind, fire an action) but not to the wheel — scrolling the
resource list changes the entire view, which was too easy to trigger by
accident while reaching for the table.

**Double-clicking** a table row opens it, the same as `enter`: logs when the
kind has them, describe otherwise — or, on the Namespaces table, switching to
that namespace and showing its pods.
