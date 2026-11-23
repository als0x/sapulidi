# Known limits of v1, and the next version's list

One line per item. "FR1 n" is item n of LIST in `docs/reviews/FR1-final-review.md`. None of these loses user data
unattended; each is a disk leak, a missing hardening step or an accepted limit.

## From the final review

- FR1 2, a kill between the rename and the `remove-start` log line: `remove-start` is logged after the rename, so a
  `.sapulidi-removing-*` left in a nested parent (for example `root/app/`) is never scanned by `finish`, and log
  rotation keeps one `.1` file. Disk leak, not data loss. Next: log the planned name before the rename.
- FR1 4, rules added by writing files: an agent with a file-write tool in its host can write `<config>/packs/*.toml`
  or `applied.toml` directly, which the terminal tier cannot prevent; the host's tool permissions are the barrier.
  Next: record a fingerprint of user packs at consent and treat unknown user rules as waiting.
- FR1 5, `mcp-runtime` in the caches category: a user pack may attach any known tool and action (a whole-wipe
  `go clean -cache`) to it, and it runs weekly outside the low-disk tier. Next: restrict `mcp-runtime` to prune actions.
- FR1 6, `finish` removes any `.sapulidi-removing-*` entry directly in the temp dir, roots and scratch bases with no log
  record of it. A narrow name, but the removal carries no evidence. Next: require a matching `remove-start` for
  base-level entries older than the log.
- FR1 8, a clock that jumps forward makes every age check pass. S9 still holds through git state and only rebuildable
  artefacts are at risk. Next: refuse a run whose `now` is far ahead of the last run record.
- FR1 11, the S1 scan in `tests/noremove_test.go` forbids `Remove`, `RemoveAll`, `Unlink`, `Rmdir` and `Unlinkat`
  only; `os.Rename` over an existing target, `os.Truncate` and exec of `git clean` or `find -delete` would pass it.
  Today the only `Rename` outside the allowed packages is the caches stamp file.
- FR1 12, the auto-rule revision golden fingerprints pack data only: widening the Go logic of an auto category
  (`temp.go`, `agent/scratch.go`) does not bump a revision and so does not wait for consent (S2). Next: a category
  logic version in the key, or a code-owner checklist line.
- FR1 13, `guard.Deny` with a temp dir of `/` cannot be reached (`platform.ownedDir("/")` is false for a non-root user,
  S10 refuses root, `temp.bases` drops `/`); the lexical Home/Base comparison in `baseAllowed` fails closed; the
  `isOpenProc` 100 ms wait only costs time; the Deny-level scratch-base gap is covered by Place and `baseAllowed`.

## Residual races and accepted decisions

- Between the last re-check and the rename (S8) a file can still be created or a symlink swapped in a parent; the
  rename then moves what is there. Place and the parent check narrow the window to microseconds; it is not closed.
- Two runs that race on one candidate end with ENOENT on the rename, reported as failed. One lock per state dir
  prevents it; no data is lost.
- An agent that drives a pty could fake a terminal for the terminal tier (`procs stop`, adding a rule,
  `uninstall --all`); the host's permission prompts are the real barrier.
- Ignored files inside a linked worktree are deleted with it (S9 protects tracked, untracked, stashed and unpushed
  work, not ignored files); this is an accepted decision.

## Added in v1.1

- `exclude.toml`: an agent with a file-write tool can edit it directly. Removing an exclusion through the CLI is
  terminal tier, but the file itself is not protected; the host's tool permissions are the barrier.
- The TUI is not a sandbox. It is a screen for a person in a terminal; what it deletes still goes through the engine
  and its guards, but anything that can drive the terminal can drive the TUI.
- `statx` birth time can be unknown on some filesystems; an age check that needs it then treats the age as unknown
  and keeps the entry.
- A pty-driving agent can fake a terminal, so the terminal tier (including `Explicit` runs and the TUI) rests on the
  host's permission prompts, as before.

## Auto-bucket proofs (audit after FR1 M1)

Every auto category and what its proof is, so no auto rule rests on a name or an age alone:

- `temp` (every rule of `packs/temp.toml`): name, age, not open, owned, and no git repository anywhere inside the
  entry (`guard.HoldsRepo`, Scan and Proof). Fixed in FIX3.
- `agent-scratch` (`claude-scratch`, `codex-tmp`): names, age, transcript inactivity, and no git repository inside
  (`guard.HoldsRepo`, Scan and Proof, both locations). Fixed in FIX3.
- `worktree-prune` (`prune`): removes only git's own administrative entries of worktrees whose folder is gone; the
  Proof rebuilds the exact command and re-checks every prunable entry (`folderGone`), so it never touches a working
  tree. Checked, no change.
- `test-artefacts` (`test-output`) and the auto rules of `packs/python.toml` (`pycache`, `pytest-cache`, `mypy-cache`,
  `ruff-cache`, `hypothesis`): kind `project`, proved by `guard.ProjectArtifact` (a project marker next to it, inside a
  git work tree that is not a linked worktree, git-ignored, no tracked file, no nested repository). Checked, no change.
- Optional categories are not auto: they need an explicit opt-in, and every one of them still passes S5.

## Uninstall `--all` checks

`uninstall --all` refuses, before removing anything, when the state, config or data path is itself a symlink, or an
existing directory holds nothing only sapulidi writes (state: `consent.json`, `log.jsonl` or `lastrun.json`; config:
`config.toml` or `applied.toml`; data: `bin/` with a version-named folder or the launcher's `.lock`). A state or data
directory that lost those files (for example a second `--all` after a partial one) has to be removed by hand.

## What the git hardening covers, and what it does not

Every git call on a repository goes through `guard.GitArgv` (guard, roots, engine, project, worktrees). It runs git with
`-c core.fsmonitor=false -c core.hooksPath=/dev/null -c core.pager=cat`, and the runner (`execx.Real`) sets
`GIT_CONFIG_NOSYSTEM=1` for a git child. That blocks a repository's `core.fsmonitor` command, hooks and pager. It is
not a sandbox:

- A filter driver (`filter.<name>.clean` or `.process` in `.git/config`, plus a `.gitattributes` in the repository) can
  still run during `git status` on racy-clean files.
- `core.attributesFile`, `diff.external` and textconv drivers are not covered.
- The same exposure exists for running git in any untrusted repository; sapulidi runs git only in repositories under
  the user's own roots and the worktrees of those.
- The user's global git config is kept on purpose (its ignore rules matter), so a hostile global config is trusted.

Git calls that do not go through `guard.GitArgv`:

- `sapulidi explain` (`internal/report/explain`) builds its own prefix: `env GIT_CONFIG_NOSYSTEM=1 git -c
  core.fsmonitor=false -c core.hooksPath=/dev/null -c core.pager=cat`, the same hardening. It needs `env` because it
  runs outside the guard's runner; next: call `guard.GitArgv` and drop the copy.
- `sapulidi doctor` runs `git --version`: no repository is involved, so nothing in a repository's config can apply.
- `scripts/release.sh` runs git in the maintainer's own checkout; it is not part of the product.
- Test helpers (`internal/testutil`, `*_test.go`) run git only inside `t.TempDir()`.
