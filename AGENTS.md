# sapulidi

A Go binary (`cmd/sapulidi`) plus a multi-host plugin that removes what coding agents and toolchains leave
behind on macOS and Linux (WSL). User guides are in `docs/` (`packs.md`, `wsl.md`, `known-limits.md`); the design
and planning notes are kept out of the repository.

## Layout

- `cmd/sapulidi/` main; `internal/cli/` command dispatcher (one `cmd_<name>.go` per command)
- `internal/core/` shared contracts (Candidate, Env, Category, Rule, registries)
- `internal/execx/` real command runner and process start time
- `internal/testutil/` test helpers: `TempHome`, `MakeRepo`, `Touch`, fakes
- `tests/` repo-wide tests (no machine paths, no stray deletions, contracts) and `tests/run.sh`

## Commands

- `bash tests/run.sh` is the one check: gofmt, `go vet` for linux and darwin, `go test ./...`, `tests/sh/test_*.sh`.
  It must pass before every commit.

## Rules

- One third-party dependency: `github.com/BurntSushi/toml`. No cgo. Targets: darwin and linux, arm64 and amd64;
  OS-specific code goes in `_darwin.go` / `_linux.go` files.
- Tests never touch the real home, temp dir, scheduler or any real repository: use `testutil.TempHome(t)`,
  `testutil.FakePlatform`, `testutil.FakeRunner`. `git` runs only inside `t.TempDir()`.
- No machine-specific value in the repository (no user home paths, owner names, private project names).
- The never bucket and the deny list are code; no config, pack or proposal can enable them.
- Every removal of user data goes through `internal/engine`. `os.Remove`/`os.RemoveAll` are allowed only in
  engine, testutil, state, schedule, agent, delivery, install and `_test.go` files. `internal/install` removes only
  the files recorded in its own `agents.json` and the empty directories that held them, in one function.
- Builds are reproducible: `-trimpath -buildvcs=false`, `CGO_ENABLED=0`.
- Output: human text by default; `--json` prints exactly one JSON document. Exit codes: 0 ok, 1 refused or
  failed, 2 usage error.
- Commit messages: short imperative subject; no ticket ids, no co-author lines. English everywhere.
