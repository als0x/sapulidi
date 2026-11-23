# sapulidi

Removes what coding agents and toolchains leave behind on macOS and Linux (WSL): agent scratch folders, build
output, package caches, stale worktrees, crash dumps and temp files. It is a small Go binary plus a plugin for
several coding agents. Only the binary deletes, and every deletion passes the same safety checks: a path is removed
only when it can be rebuilt, nothing uses it, and you did not choose it on purpose.

## Install

The plugin ships a launcher, not a binary. On first use the launcher downloads the binary of its pinned version
from the GitHub release, checks its SHA-256 against `checksums.txt` and installs it under your data directory.
Nothing runs unverified.

| Host | How |
|---|---|
| Claude Code | `/plugin marketplace add https://github.com/als0x/sapulidi`, then `/plugin install sapulidi@als0x-sapulidi` |
| Codex | install the plugin from this repository (`.codex-plugin/`) |
| Cursor | install the plugin from this repository (`.cursor-plugin/`) |
| Antigravity | install the plugin from this repository (`.antigravity-plugin/`) |
| OpenCode, Pi | clone the repository and link `skills/sapulidi` into the skill directory; see `.opencode/INSTALL.md` and `.pi/INSTALL.md` |

## First run

Start a session; sapulidi says it is not set up. Run:

```
/sapulidi setup
```

It shows the roots it will scan, every category and a dry run, and asks for your consent. Until you consent,
scheduled and hook runs only log what they would do. It then offers to install the schedule.

## What it removes and what it never removes

| Bucket | Meaning | Default |
|---|---|---|
| auto | pure leftovers, rebuildable, past an age, not in use | on after your consent |
| optional | rebuildable but costly to rebuild, or ownership less clear | off; enable per category with an age |
| never | not rebuildable or chosen by you | never removed, whatever the config says; may appear in reports |

Never removed, in every pack: secrets and credentials (`.env*`, `*.pem`, `*.key`, `id_*`, `*.tfstate*`; a
directory containing one is kept whole), source, `.git`, files tracked by git, unsaved work (dirty, untracked,
stashed or unpushed), container volumes, models, agent memory and settings, live sessions, `Downloads`, `Trash`,
`Documents`, `Desktop`, anything outside your roots, anything needing `sudo`, network mounts and other filesystems.

The never bucket and the deny list are code. No config, pack or proposal can switch them off. The built-in packs
are listed in `packs/`; see `docs/packs.md` for their format. An agent that has a file-write tool in its host can
write `<config>/packs/*.toml` or `applied.toml` directly, which the terminal tier cannot prevent; the host's tool
permissions are the barrier there.

## Scheduled run and agent

`sapulidi schedule install` adds an OS job that runs at low priority every 30 minutes. `sapulidi pause` stops every
scheduled and hook run; `sapulidi resume` restarts them. If no host session has touched sapulidi for 30 days, the
schedule falls back to a dry run, so removing the plugin cannot leave a job deleting things for long.

The scheduled agent is optional and off by default. It judges what rules cannot (a directory that grew fast and
matches no pack, orphaned worktrees) and writes proposals; it never deletes. It runs headless with a read-only
tool allowlist and a cost cap of `max_usd` per run (default 0.25 USD). Review proposals with `/sapulidi review`;
applying one goes through the same guards as any removal.

## Verify a binary

Releases are built by GitHub Actions from a tag, with reproducible flags (`-trimpath -buildvcs=false`,
`CGO_ENABLED=0`), a `checksums.txt` and a build provenance attestation:

```
gh attestation verify sapulidi_<version>_<os>_<arch> --repo <owner>/sapulidi
```

`<owner>` is the account of the repository you installed from (the one in the marketplace URL above).

## Uninstall

`sapulidi uninstall` removes the schedule and the downloaded binaries; `sapulidi uninstall --all` also removes
config and state. Then remove the plugin from your host.

## Status

Tested: the launcher, the binary and the full flow (setup, plan, run, consent, schedule install and remove on launchd, check, uninstall) on macOS arm64; the same flow inside an Alpine arm64 container as a non-root user (cron); and the plugin installed in Claude Code 2.1.330 from a local marketplace, with a headless session in which the SessionStart hook's line reached the model and the skill loaded as `/sapulidi` and `/sapulidi:sapulidi`. Not yet run: a download from a real GitHub release, `gh attestation verify`, an interactive real Claude Code session driven by a person, and WSL itself. Never run: Codex, Cursor, Antigravity, OpenCode, Pi (their manifests and hooks are untested).

See `CONTRIBUTING.md` to build, test or add a pack.
