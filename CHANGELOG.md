# Changelog

Every released version, in the same shape: `Added`, `Changed`, `Fixed`,
`Performance` — only the headings that have entries. One line per change; the
reasoning lives in the release notes each section links to.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Pre-1.0, a minor bump is a feature release and a patch bump is fixes.

Cutting one is four steps, in order — see
[docs/dev.md](docs/dev.md#cutting-a-release).

## [Unreleased]

Nothing yet.

## [v0.6.0] — 2026-09-16

[Release notes](docs/releases/v0.6.0.md)

### Added

- Log viewer parses structured lines: JSON and logfmt records render as
  `time LEVEL message │ k=v`, with the duplicate record timestamp dropped.
- `f` filters log lines live (`-term` excludes), `w` cycles the level floor
  (all → INFO → WARN → ERROR), `t` shows the raw lines again.
- Lens actions can declare `params`: values collected in a form before the
  action runs, with typeahead suggestions from a JSONPath on the object or from
  a related kind via the pack's own edges.
- `confirmValue` lets a typed confirmation ask for the value at risk — fencing
  asks for the instance, not the cluster.
- CNPG: restore to a new cluster with a point-in-time recovery target, scale
  instances, scale a pooler.
- CNPG: `WAL` column reading the `ContinuousArchiving` condition, and `NODES`
  reading `.status.topology.nodesUsed`.
- CNPG: Databases, Publications, Subscriptions and Image Catalogs — the
  declarative CRDs from 1.25 on.
- Demo fixtures for every CNPG kind.

### Changed

- CNPG fence and promote pick an instance from the cluster's own
  `.status.instanceNames` instead of a free-text box.
- A lens kind's tenth action is reachable as `0`. CNPG's tenth is Wake, so
  hibernating was reachable and un-hibernating was not.

### Fixed

- A create action's confirm modal no longer paints its manifest over the panes
  behind it. The kubectl heredoc is several lines in one string, and the modal
  neither split it nor reserved rows for it.
- CNPG Poolers grade against the real `active|paused|inactive|failed` enum. The
  table named `"Pooler is ready"`, which CNPG never writes, so every healthy
  PgBouncer rendered amber.
- A parameter default is rendered as a template, so `"{{.Name}}-restore"` no
  longer reaches the manifest with its braces intact.
- A `create` body drops empty fields before it is sent. An unset recovery
  target used to be written as an empty one, which CNPG must interpret and
  which leaves the cluster never finishing bootstrap.

## [v0.5.0] — 2026-09-16

[Release notes](docs/releases/v0.5.0.md) — `v0.4.0` was never cut; everything
planned for it ships here.

### Added

- cert-manager pack: Certificates, CertificateRequests, Issuers, ClusterIssuers,
  with an `EXPIRES` column answering "which certificate dies first".
- Flux pack: Kustomizations, GitRepositories, HelmRepositories, HelmReleases —
  reconcile, suspend and resume without the `flux` binary.
- Gateway API pack: GatewayClasses, Gateways, HTTPRoutes, GRPCRoutes, with
  `ATTACHED` per listener.
- `X` draws the ArgoCD and Kargo graph as a tree, three hops deep, each object
  carrying its own graded cells. Generic over every lens pack.
- Four GitOps edges: stage→stage (the promotion DAG), stage→warehouse,
  application→appproject, application→applicationset.
- `ctrl+y` exports the selected object's YAML, its describe output and the
  visible table to a file, with credentials stripped.
- `format: until` for lens columns — a future instant as a duration.

### Changed

- Every graded cell leads with a mark as well as a colour: `+` ok, `!` warn,
  `x` error, `?` unknown. Width is reserved up front, so the column no longer
  jitters as a status changes.
- Status hints give up width to a toast while one is showing.
- `ctrl+y` is in `cmd/shot`'s key table, so the new binding replays headlessly.

### Fixed

- Export redacts credentials matched **normalised**, so a CR's `clientSecret`
  rendered by kubectl's generic describer as `Client Secret:` no longer leaks.
- Export redacts credentials passed as container flags (`--client-secret=…`),
  which have no `key: value` shape to match on.
- Toasts shorten from the middle, keeping the filename at the tail of a path.
- Wide runes no longer break column alignment — a cell is measured by display
  width, not rune count.
- Tree walk: status lookup no longer fails for every kind without a `NAMESPACE`
  column, and the demo backend no longer draws a root as its own descendant.

### Performance

- Frames are ~85% cheaper and allocate ~93% less per repaint: ANSI escape pairs
  are cached per fg/bg/bold combination instead of re-rendered per cell.
  Frames stay byte-identical, verified with `cmd/shot`.
- `BenchmarkView` and `BenchmarkKeypressFrame` cover the render hot path, which
  previously had no benchmark.

## [v0.3.0] — 2026-09-15

[Release notes](docs/releases/v0.3.0.md)

### Added

- k3s pack: HelmCharts and their install Jobs, upgrade Plans mid-roll.
- Rancher and Fleet packs: Fleet GitRepos and Bundles, Rancher's `c-m-xxxxx`
  clusters under their real names, force-resync and index refresh.
- VictoriaMetrics pack.
- Sidebar badges count lens kinds, instead of staying blank until first opened.

### Fixed

- `just test-perf` was matching `TestRowCount` by prefix and skipping guards.
- A column identical on every row (a chart-repo URL) no longer takes width from
  the columns that differ.

### Changed

- A test runs the real shipped packs through the registry, so a pack that stops
  loading fails the build.
- `docs/lenses.md` documents the filter form.

## [v0.2.0] — 2026-09-15

[Release notes](docs/releases/v0.2.0.md)

### Added

- Lens packs: ArgoCD, Kargo, CNPG, Longhorn and Traefik get real tables — sync
  and health, promotion phase, cluster phase, volume robustness — with the
  daily verbs on the keys beside them.
- Discovery gate: nothing appears on a cluster that does not run it.
- The UI states, not just the docs, that ArgoCD sync bypasses `argocd-rbac-cm`
  and that Kargo promote needs a virtual verb.

### Fixed

- A service port cell like `80/TCP` is no longer coloured as a failure.
- The cursor follows its object across a re-sort.
- A broken lens pack now says so instead of silently never appearing.

## v0.1.4 — 2026-09-15 (notes only, never tagged)

[Release notes](docs/releases/v0.1.4.md)

### Changed

- The demo is a context, not a fallback: `k10s demo` opens on it, `/demo`
  switches to it, `:ctx` leaves it, and a `DEMO` marker rides in the header so
  no published frame can pass for a real cluster.
- A failed connection shows a "No cluster" panel instead of sample data.
- `:mouse` is now `/mouse` — toggling capture is k10s, not the cluster.

### Fixed

- The mouse comes back after `$EDITOR` exits.
- `$EDITOR` is parsed with quotes and backslash escapes honoured.
- Startup no longer reports a connection it does not have.

## v0.1.3 — 2026-09-15 (notes only, never tagged)

[Release notes](docs/releases/v0.1.3.md)

### Added

- k9s `plugins.yaml` support: scoped commands, k9s shortcut notation, `confirm`,
  `dangerous` and `background`, and the `$NAME` / `$NAMESPACE` / `$CONTEXT`
  variable set. Applicable plugins are appended to the Actions pane.
- Two load locations: `~/.k10s/plugins.yaml`, then every `.yaml`/`.yml` beneath
  the plugins directory.

### Fixed

- A malformed plugin is skipped by filename and never costs you the TUI.

## v0.1.2 — 2026-09-15 (notes only, never tagged)

[Release notes](docs/releases/v0.1.2.md)

### Added

- Custom themes: twelve `#RRGGBB` fields in a YAML file under `~/.k10s/themes`,
  loaded at startup into the same picker as the built-ins, with live preview.

### Fixed

- The theme picker scrolls with an unbounded list.
- `theme → x (saved)` appears only when the write actually succeeded.

## v0.1.1 — 2026-09-15 (notes only, never tagged)

[Release notes](docs/releases/v0.1.1.md)

### Added

- One-line install (`install.sh`).
- 30 resource kinds, in foldable groups.
- `/` for k10s commands, `:` for the cluster; every kind answers to its plural
  and singular, and `:po kube-system` scopes a kind to a namespace.
- Anything that is not a `/` or `:` command runs as a shell command.

### Changed

- Namespace and context moved to `:ns` / `:ctx`.
- No setup screen.
- AI is switched off in this build.

## v0.1.0 — 2026-09-15 (notes only, never tagged)

[Release notes](docs/releases/v0.1.0.md)

The first cut of the Kubernetes terminal UI you can click. Published notes
only: `v0.2.0` was the first tag to reach GitHub Releases, so the `v0.1.x`
sections above it record work that shipped inside it.

### Added

- Mouse everywhere: rows, panes, `[ zoom ]`, sort headers.
- An Actions pane listing what applies to the selection, so nothing hides
  behind a memorised key.
- `ctrl+p` searches resource kinds and objects in one box.
- Instant startup: zero watches registered up front.
- `ctrl+a` for AI with the current screen as context.
- Logs (`l`), interactive exec, and port-forward.
- `/update` installs the newest release over the running binary.
- Seven themes with live preview via `/theme`.
- `ctrl+s` copy mode, which releases the mouse to the terminal.

[Unreleased]: https://github.com/0x01001011/k10s/compare/v0.6.0...HEAD
[v0.6.0]: https://github.com/0x01001011/k10s/compare/v0.5.0...v0.6.0
[v0.5.0]: https://github.com/0x01001011/k10s/compare/v0.3.0...v0.5.0
[v0.3.0]: https://github.com/0x01001011/k10s/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/0x01001011/k10s/releases/tag/v0.2.0
