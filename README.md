<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/readme/logo-dark.svg">
    <img src="assets/readme/logo-light.svg" alt="Sapu Lidi" width="220">
  </picture>
</p>

<p align="center">
  <b>Cleans up what coding agents and toolchains leave behind on macOS and Linux.</b><br>
  Agent scratch folders, build output, package caches, stale worktrees and crash dumps: removed only when they can be rebuilt.
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#quick-start">Quick start</a> ·
  <a href="#what-it-removes-and-what-it-never-removes">Safety</a> ·
  <a href="docs/tui.md">TUI</a> ·
  <a href="docs/packs.md">Packs</a>
</p>

<p align="center">
  <a href="https://github.com/als0x/sapulidi/releases/latest"><img src="https://img.shields.io/github/v/release/als0x/sapulidi" alt="Latest release" height="20"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/als0x/sapulidi" alt="MIT license" height="20"></a>
  <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20WSL-555" alt="Platforms: macOS, Linux, WSL" height="20">
</p>

---

## Why

- **One deleter.** A small Go binary does every removal. The plugin for your agent only asks it to.
- **Rebuildable or untouched.** A path goes only when it can be rebuilt, nothing uses it, and you did not choose it on purpose. Secrets, source, `.git`, unsaved work and anything outside your project roots are never removed, whatever the config says.
- **You see it first.** `sapulidi plan` shows what a run would remove and what it keeps. Nothing is deleted before you consent.
- **Every row has a label.** **safe** goes on the schedule once you consented, **recommended** only if you also allowed it, **your call** only when you pick it, and **protected** stays.
- **Works with your agent.** Plugins for Claude Code, Codex, Cursor, Antigravity, OpenCode and Pi, with 24 built-in packs for the tools they leave files behind (Node, Rust, Python, JVM, Dart, Swift, containers and more).
- **Checked downloads.** The launcher and `install.sh` verify every binary against the SHA-256 in [`checksums.txt`](checksums.txt) before it runs.

## Install

One command, pinned to a release tag. It verifies the binary and links it into `~/.local/bin`:

```sh
curl -fsSL https://raw.githubusercontent.com/als0x/sapulidi/v0.2.0/install.sh | sh
```

Prefer to read the script first?

```sh
curl -fsSL https://raw.githubusercontent.com/als0x/sapulidi/v0.2.0/install.sh -o install.sh
less install.sh
sh install.sh            # sh install.sh --dry-run shows what it would do
```

Everything after `--` goes to `sapulidi`, so you can install into an agent and start the schedule in one go:

```sh
curl -fsSL https://raw.githubusercontent.com/als0x/sapulidi/v0.2.0/install.sh | sh -s -- --agents claude --schedule 30m
```

### As a plugin

| Host | How |
|---|---|
| Claude Code | `/plugin marketplace add https://github.com/als0x/sapulidi`, then `/plugin install sapulidi@als0x-sapulidi` |
| Codex, Cursor, Antigravity | install the plugin from this repository (`.codex-plugin/`, `.cursor-plugin/`, `.antigravity-plugin/`) |
| OpenCode, Pi | clone the repository and link `skills/sapulidi` into the skill directory; see `.opencode/INSTALL.md` and `.pi/INSTALL.md` |

The plugin ships a launcher, not a binary. On first use it downloads the binary of its pinned version from the GitHub release, checks its SHA-256 and installs it under your data directory.

## Quick start

```sh
sapulidi            # open the TUI: scan, pick, confirm, delete
sapulidi plan       # dry run: what would a run remove, and what does it keep
sapulidi setup      # roots, categories and a dry run; --yes records your consent
```

Inside an agent session, run `/sapulidi setup` once. It shows the roots it found (from the folders your coding agents worked in), every category and the plan by label, and asks for your consent. Until you consent, scheduled and hook runs only log what they would do.

| Label | What happens to it |
|---|---|
| safe | the schedule removes it once you consented (`setup --yes`) |
| recommended | the schedule removes it only if you also allowed it (`setup --yes --recommended`, later `consent --yes --recommended`); `run --recommended` lists these rows, asks, and removes them once |
| your call | listed only; it goes when you pick it in the TUI, or after you consent to its rule (`sapulidi consent`) |
| protected | not removable now: a guard or your exclusion keeps it |

The TUI opens on **places**: the folders where something was found, with what is ready, what is optional and what a guard keeps. This is a screen from the test fixtures (sample paths, not a real machine):

```text
+- summary --------------------------------------------------------------------+
| Free now: 1.9 GB automatic | 762.9 MB if you choose | 9 protected            |
| to remove: 0 B, 0 of 6 selected | excluded: 2                                |
| agents installed: 2 | schedule: every 30m, next 14:30                        |
| disk free: 37.3 GB -> 37.3 GB                                                |
| not covered by any rule: searching...                                        |
+------------------------------------------------------------------------------+
         ready  optional place                                  kept updatedAt
  ready = removable now | optional = you pick it | kept = protected
>[ ]    1.1 GB         - ~/proj/                                   1 2026-11-21
 [ ]  762.9 MB         - ~/.nvm/versions/node/                     1 2026-11-20
 [ ]         -  762.9 MB ~/old/                                    - 2025-02-03
 [ ]    4.8 MB         - /private/tmp/claude-1000/                 6 2026-11-15
enter open | space select | d delete | g protected | c view | ? help | q quit
```

`enter` opens a place, `space` selects, `d` shows a confirmation that lists every selected row. Keys, markers and columns are in [docs/tui.md](docs/tui.md), and `?` opens the same help inside the TUI.

## Commands

| Command | Does |
|---|---|
| `sapulidi` | the TUI (needs a terminal) |
| `plan`, `run` | dry run, and the real run that rules and your consent allow |
| `report` | what uses the disk: categories, models, big folders no rule covers |
| `explain <path>` | size, owner, git state, matching rule and guard for one path |
| `exclude <path>` | never touch that path (`--list`, `--remove <path>`) |
| `schedule`, `pause`, `resume` | the scheduled run, and a switch that stops every run |
| `consent` | list the rules that wait for your OK, record it, or take it back (`--revoke`) |
| `update` | install the latest release (`--check-only` only asks) |
| `agents` | list agent tools; `--agents <name>\|all` installs into one, add `--remove` to undo |
| `uninstall` | remove the schedule and downloaded binaries; `--all` also config and state |

`sapulidi help` lists the rest. Every command takes `--json` and prints one JSON document.

## What it removes and what it never removes

| Bucket | Meaning | Default |
|---|---|---|
| auto | pure leftovers, rebuildable, past an age, not in use | on after your consent |
| optional | rebuildable but costly to rebuild, or ownership less clear | listed; removed only with consent to the rule, or when you pick it |
| never | not rebuildable or chosen by you | never removed, whatever the config says |

Never removed, in every pack: secrets and credentials (`.env*`, `*.pem`, `*.key`, `id_*`, `*.tfstate*`; a directory containing one is kept whole), source, `.git`, files tracked by git, unsaved work (dirty, untracked, stashed or unpushed), container volumes, models, agent memory and settings, live sessions, `Downloads`, `Trash`, `Documents`, `Desktop`, anything outside your roots, anything needing `sudo`, network mounts and other filesystems.

The never bucket and the deny list are code. No config, pack or proposal can switch them off. A build folder that holds a nested git repository is removed only when that repository is clean and fully pushed. Pack format: [docs/packs.md](docs/packs.md).

## How it works

```text
 agent plugin ──► launcher ──► sapulidi binary ──► engine ──► guards ──► delete
 (skill, hooks)   (verifies     (packs: what       (only      (deny, place, secret,
                   SHA-256)      can be rebuilt)    deleter)   fresh, in-use, proof)
```

Packs say what is a leftover. The engine is the only code that deletes, and every candidate passes the same guards again right before removal, so a path that changed since the scan is kept.

## Scheduled run and agent

`sapulidi schedule install` adds an OS job that runs at low priority every 30 minutes. If no host session has touched sapulidi for 30 days, the schedule falls back to a dry run, so removing the plugin cannot leave a job deleting things for long. `sapulidi pause` stops every scheduled and hook run.

The scheduled agent is optional and off by default. It judges what rules cannot (a directory that grew fast and matches no pack, orphaned worktrees) and writes proposals; it never deletes. It runs headless with a read-only tool allowlist and a cost cap of `max_usd` per run (default 0.25 USD). Review proposals with `/sapulidi review`; applying one goes through the same guards as any removal.

## Updates

A release build asks GitHub once a day whether a newer release exists. When there is one, `sapulidi check` (at session start), `sapulidi version` and the TUI show one line that says how to get it.

- Installed with `install.sh`: `sapulidi update` shows where it downloads from, asks, verifies the checksum and installs. To let the scheduled run do it, set `auto = true` under `[update]` in `config.toml`.
- Installed as a plugin: update the plugin in your host. In Claude Code that is `/plugin update sapulidi@als0x-sapulidi`.
- Claude Code plugin auto-update: open `/plugin`, go to **Marketplaces**, choose `als0x-sapulidi` and enable auto-update. These menu names are not verified in a live session and may differ in your version.

To switch the check off, set `check = false` under `[update]` in `config.toml`. What the check sends and what an update trusts: [docs/known-limits.md](docs/known-limits.md#updates-030).

## Limits

- An agent that has a file-write tool in its host can write `<config>/packs/*.toml` or `applied.toml` directly, which the terminal tier cannot prevent; the host's tool permissions are the barrier there.
- Containers and image or builder prunes are reported only; the TUI does not delete them.
- A build folder holding a partial clone is kept.
- More in [docs/known-limits.md](docs/known-limits.md); Windows goes through WSL ([docs/wsl.md](docs/wsl.md)).

## Verify a binary

Releases built by GitHub Actions from a tag carry a build provenance attestation. Version 0.1.0, and any release built locally with `scripts/release.sh` (reproducible flags `-trimpath -buildvcs=false`, `CGO_ENABLED=0`), do not; the launcher and `install.sh` check every download against the `checksums.txt` committed in this repository either way. To check an attestation:

```sh
gh attestation verify sapulidi_<version>_<os>_<arch> --repo <owner>/sapulidi
```

`<owner>` is the account of the repository you installed from (the one in the marketplace URL above).

## Uninstall

`sapulidi uninstall` removes the schedule and the downloaded binaries; `sapulidi uninstall --all` also removes config and state. Then remove the plugin from your host.

## Status

Tested: on macOS arm64, the launcher, the binary and the full flow (setup, plan, run, consent, schedule install and remove on launchd, check, uninstall); the TUI on a pseudo-terminal with a sandbox home (select, delete, quit, terminal restored) and read-only against a real home; and `sapulidi --agents claude` installed into and removed from a real Claude Code, with its plugin and marketplace lists identical afterwards. On Linux (Debian arm64 container, non-root): the same flow, the pty tests, a TUI scenario in a pty, and `install.sh` under dash (74 checks); in an Alpine arm64 container: the pty tests. The launcher and `install.sh` have downloaded and verified the real 0.1.0 and 0.2.0 releases from GitHub. Not yet run: `install.sh` under BusyBox ash (the Alpine image has no curl and no bash), `gh attestation verify` (0.1.0 and locally built releases have no attestation), WSL itself, and a real Claude Code session driven by a person through the `/plugin` menu. Never run: Codex, Cursor, Antigravity, OpenCode, Pi (their manifests and hooks are untested).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) to build, test or add a pack. The one check is `bash tests/run.sh`.

## License

[MIT](LICENSE)
