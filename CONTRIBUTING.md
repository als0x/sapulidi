# Contributing

## Tests

`bash tests/run.sh` is the one check: gofmt, `go vet` for linux and darwin, a reproducible build, `go test ./...`
and `tests/sh/test_*.sh`. It must pass before every commit. Work in a branch; write the test first, see it fail,
then make it pass.

Tests never touch your real home, temp dir, scheduler or any real repository. Use `testutil.TempHome(t)`,
`testutil.FakePlatform` and `testutil.FakeRunner`; `git` runs only inside `t.TempDir()`.

## Rules

These come from `AGENTS.md`:

- One third-party dependency: `github.com/BurntSushi/toml`. No cgo. Targets are darwin and linux, arm64 and amd64;
  OS-specific code goes in `_darwin.go` and `_linux.go` files.
- No machine-specific value in the repository: no user home paths, owner names or private project names.
- The never bucket and the deny list are code. No config, pack or proposal can enable them.
- Every removal of user data goes through `internal/engine`. `os.Remove` and `os.RemoveAll` are allowed only in
  engine, testutil, state, schedule, agent, delivery and `_test.go` files.
- Builds are reproducible: `-trimpath -buildvcs=false`, `CGO_ENABLED=0`.
- Output is human text by default; `--json` prints exactly one JSON document. Exit codes: 0 ok, 1 refused or
  failed, 2 usage error.
- Commit messages: a short imperative subject; no ticket ids, no co-author lines. English everywhere.

## Adding a pack

A pack is a TOML file in `packs/`; no Go is needed. See `docs/packs.md` for the format and an example.

1. Create `packs/<pack>.toml`, one rule per artefact, with a marker next to every per-project artefact.
2. Add fixtures under `internal/categories/<category>/testdata/<pack>/`: a fake project tree with the expected
   candidates and the expected keeps.
3. If you add or widen an auto rule, update `packs/testdata/autorules/<pack>.golden` and bump the rule's `rev`.
4. Run `bash tests/run.sh`.

## Releases

The owner runs `scripts/release.sh <version>`, which bumps the launcher and every manifest, builds the four
binaries, writes `checksums.txt` and runs the checks, then prints the commit, tag and push commands. Pushing a
`v*` tag starts the release workflow. Nothing else publishes.

The binaries check for updates in one GitHub repository, set at build time: `SAPULIDI_REPO=owner/repo` when set,
otherwise the repository the `origin` remote names. The script refuses to run with neither.

The workflow refuses a tag whose commit is not on `main`, runs `bash tests/run.sh`, builds with the toolchain named
in `go.mod`, compares the checksums with the committed `checksums.txt`, attests the binaries and `checksums.txt`,
and creates a **draft** release. Look at the draft, then publish it by hand.

### When CI's checksums differ from the committed ones

The release job fails and publishes nothing. Copy the checksums it computed (printed in the job log by the diff)
into `checksums.txt`, commit, delete the tag locally and on the remote, and tag again. If it keeps happening,
compare `go version` locally with the `toolchain` line of `go.mod`; `scripts/release.sh` refuses to build with a
different Go, and builds with `GOFLAGS` unset, `GOAMD64=v1` and `CGO_ENABLED=0`, as CI does.

## Owner setup

The repository cannot enforce these GitHub settings; the owner sets them once:

- A protected-tag ruleset for `v*`: only the owner can create, move or delete release tags.
- Required reviewers on the `release` environment, which the release job runs under.
- Branch protection on `main`: pull requests and the CI jobs required, no force pushes.
