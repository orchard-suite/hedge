# AGENTS.md

hedge keeps an estate's own names out of repositories that may not hold
them. `README.md` says what it does; `docs/register.md` holds the config
and register formats; `docs/fixtures.md` is the made-up world every test
and example here uses.

## Layout

| Path | Holds |
| --- | --- |
| `cmd/hedge` | The program's entry point |
| `internal/match` | The index file and the scanner: the hot path of every hook |
| `internal/repo` | Finds a path's repository and names it by its remote, without running git |
| `internal/check` | Findings, `.hedgeignore`, and printing |
| `internal/gitscan` | Reads the staged change, a message, a push, or a history |
| `internal/claude` | The Claude Code PreToolUse hook |
| `internal/register` | The config, the register, the harvest, and compiling the index |
| `internal/cli` | The commands; `e2e_test.go` drives real git through the hooks |
| `hooks/`, `skills/`, `.claude-plugin/` | The plugin |

## Rules

- This repository names no estate. Every name in a test or a doc comes from
  `docs/fixtures.md`.
- The hook runs on every write. Keep `internal/match` free of regular
  expressions and of allocation per word, and run `make bench` before and
  after a change to it. The index format has a version; change it when the
  layout changes.
- A guard that cannot see blocks: with no index, a write into a repository
  waits for `hedge refresh`.

## Check

`make check` runs gofmt, vet, and every test, including the end-to-end
tests that build hedge and drive git through its hooks.

## Delivery

Agents push to main. Land with `make land`. Commit as
`<type>(hedge): <description>`. CI tests on GitHub's hosted runners,
versions, and publishes; never version by hand.
