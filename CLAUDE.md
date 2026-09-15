# k10s

Clickable Kubernetes terminal UI. Go 1.26, module `github.com/0x01001011/k10s`,
single static binary. Bubble Tea TUI.

## Commands

`just` is the task runner (`just` alone lists every recipe).

| Task | Command |
| --- | --- |
| Build | `just build` |
| Run from source | `just dev` |
| Run without a cluster | `just dev-demo` |
| Tests | `just test` |
| One test | `just test-one <pattern>` |
| Race detector | `just test-race` |
| Everything CI enforces | `just check` (fmt-check → vet → test) |
| Headless frame capture | `just shot 140 44 j,j,d` |

`just check` must pass before any change is considered done.

## Layout

- `main.go`, `cmd/shot` — entrypoints. `cmd/shot` renders one frame headlessly,
  so UI changes are verifiable with no TTY and no cluster.
- `internal/ui` — Bubble Tea model, update loop, views, palette, pickers.
  `model.go` and `update.go` are the hot centre.
- `internal/k8s` — cluster client, listers, store, row formatting, exec,
  port-forward, logs, metrics.
- `internal/domain` — resource kinds and ordering, shared by k8s and ui.
- `internal/mock` — offline demo backend behind `k10s demo`.
- `internal/config`, `theme`, `plugin`, `ai`, `update`, `version` — supporting
  subsystems; `examples/` holds sample theme and plugin YAML.

Deeper docs live in `docs/` — `architecture.md`, `ui.md`, `backends.md`,
`performance.md`, `dev.md`, `lenses.md`.

## CodeGraph

This repo is indexed (`.codegraph/`, gitignored). Use it **before** grep/find
when locating or understanding code:

- `codegraph_explore` MCP tool, or `codegraph explore "<symbols or question>"`
- `codegraph node <symbol>` — one symbol's source plus caller/callee trail
- `codegraph impact <symbol>` — blast radius before changing a shared function
- `codegraph affected <files...>` — which tests a change touches

The index syncs automatically after edits (see `.claude/settings.json`).
Rebuild from scratch: `codegraph index .`

## Agent skills

### Issue tracker

GitHub Issues on `0x01001011/k10s`, via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical roles, unchanged: `needs-triage`, `needs-info`, `ready-for-agent`,
`ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context — `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.

## Conventions

- Verify UI changes against a real frame (`just shot` or `just dev-demo`), not
  by reading the view code. Every screenshot in the docs comes from the binary.
- Tests sit beside their source as `*_test.go`; add one with the change.
- `internal/ui` has perf guards (`just test-perf`) — keep `View` off the
  row-building path.
