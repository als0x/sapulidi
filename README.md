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

### Install from a terminal

`install.sh` fetches the binary of one pinned version, verifies it and links it into `~/.local/bin`. Pin the URL to
a tag, and read the script first if you like:

```
curl -fsSL https://raw.githubusercontent.com/als0x/sapulidi/v0.2.0/install.sh -o install.sh
less install.sh
sh install.sh
```

Or in one line; everything after `--` goes to `sapulidi`:

```
curl -fsSL https://raw.githubusercontent.com/als0x/sapulidi/v0.2.0/install.sh | sh -s -- [flags]
```

Flags: `--agents <name>|all` installs sapulidi into an agent tool (add `--remove` to undo), `--schedule 30m` installs
the scheduled run (`--schedule off` removes it, `--schedule` alone shows it), for example
`... | sh -s -- --agents claude --schedule 30m`. `sh install.sh --dry-run` shows what it would do.

## The TUI

Run `sapulidi` with no arguments in a terminal. It scans and opens on a list of **places**: the folders where it
found something (a project, a toolchain folder), with what is ready to delete, what is optional, what a guard keeps
and when it last changed. `enter` opens a place and lists its rows; every deletion still goes through the engine and
its safety checks. The first line of the summary answers the question: what is free now automatically, what if you
choose, and how many rows are protected. `?` opens a help screen that explains the columns, the markers and the keys,
and `u` lists, read-only, the big folders that no rule covers (found with the same search as `report`; nothing there
can be selected or deleted). Keys: `up`/`down` move, `enter` open a place or a row's detail, `space` select (on a place: every
ready row in it, never the optional ones), `a` select all (on the places and inside a place: ready rows only, never the optional ones; in the flat list: every row in
view), `d` delete the selection (its confirmation scrolls with `up`/`down`, `j`/`k`, `PgUp`/`PgDn`), `x` exclude, `c` switch between the places and the flat list, `o` show optional rows in the flat list
(places always list them), `g` show what a guard keeps (guarded, with the reason; on the places it also lists
places that hold only kept rows), `/` search, `s` sort, `esc` back, `q` quit. The selection belongs to the whole
scan, not to the screen: switching views, `o`, `g`, search and sort never change it, and the confirmation lists
every selected row, including the ones the current view hides. It is for a person in a terminal; an agent uses
`plan` and `report`.

## Exclusions

`sapulidi exclude <path> [--note text]` makes sapulidi never touch that path (the same as `x` in the TUI);
`sapulidi exclude --list` shows them and `sapulidi exclude --remove <path>` needs a terminal.

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

Releases built by GitHub Actions from a tag carry a build provenance attestation. Version 0.1.0, and any release
built locally with `scripts/release.sh` (reproducible flags `-trimpath -buildvcs=false`, `CGO_ENABLED=0`), do not;
the launcher and `install.sh` check every download against the `checksums.txt` committed in this repository
either way. To check an attestation:

```
gh attestation verify sapulidi_<version>_<os>_<arch> --repo <owner>/sapulidi
```

`<owner>` is the account of the repository you installed from (the one in the marketplace URL above).

## Uninstall

`sapulidi uninstall` removes the schedule and the downloaded binaries; `sapulidi uninstall --all` also removes
config and state. Then remove the plugin from your host.

## Status

Tested: the launcher, the binary and the full flow (setup, plan, run, consent, schedule install and remove on launchd, check, uninstall) on macOS arm64; the same flow inside an Alpine arm64 container as a non-root user (cron); and the plugin installed in Claude Code 2.1.330 from a local marketplace, with a headless session in which the SessionStart hook's line reached the model and the skill loaded as `/sapulidi` and `/sapulidi:sapulidi`. Also run: the launcher downloading and verifying the real 0.1.0 release from GitHub. Not yet run: `gh attestation verify` (0.1.0 has none), an interactive real Claude Code session driven by a person, and WSL itself. Never run: Codex, Cursor, Antigravity, OpenCode, Pi (their manifests and hooks are untested).

See `CONTRIBUTING.md` to build, test or add a pack.
