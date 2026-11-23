# Linux container suite: result (PRD LX2)

Command: `bash tests/linux/run.sh` on the owner's Podman (rootless, arm64 host, Podman 5.6.2).
Overall: **FAILED** (exit 2) in the Debian image, at `tests/sh/test_install.sh` under dash.
The Alpine image passed.

## Images

| Image | Size pulled | Existed before the run | Result |
|---|---|---|---|
| `docker.io/library/golang:1.26` (Debian, `/bin/sh` = dash) | 916,243,449 bytes | no | FAILED at install.sh under dash |
| `docker.io/library/golang:1.26-alpine` (BusyBox, `/bin/sh` = ash) | 249,961,448 bytes | no | passed |

Both images were removed afterwards with `podman rmi` (the run script does this for any image not present before
the run). The other images in the owner's Podman were not touched.

## Debian pass (`inside.sh` → `as_user.sh`, user `user`, shell `/usr/bin/dash`)

- `bash tests/run.sh`: all checks passed (gofmt, vet for linux and darwin, `go test ./...`, the `tests/sh` suites;
  `all 20 codex hook checks passed`, `all 29 hook checks passed`, `all checks passed` in test_launcher).
- `sh tests/linux/pty_check.sh` (`go test -v ./internal/tui/... ./cmd/sapulidi/...`): the four pty tests passed and
  did not skip: `TestPTYEnterRestore`, `TestPTYKeysAndSize`, `TestPTYSignalsRestoreAndExit`,
  `TestTUIQuitsAndLeavesCookedMode`.
- `bash tests/linux/scenario.sh`: `scenario passed` (old go-build and old node_modules removed; young ones kept; cron
  entry installed; doctor ran; doctor warned about notify-send, the agent CLI and 7 GB free disk).
- `bash tests/linux/tui_scenario.sh`: `tui scenario passed`
  - run 1: `q` leaves the stale folder in place, exit 0, `stty -a` shows `icanon` and `echo`;
  - run 2: space, `d`, `y`, `q`, `q`: the stale folder is removed, `keep.txt` stays, exit 0, `icanon` back.
- `sh tests/sh/test_install.sh` under dash: **FAILED**.

```
tests/sh/test_install.sh: 81: Syntax error: redirection unexpected
```

Line 81 is `check "prints where and version" grep -q ... <<<"$out"`: a bash here-string. The file has a bash shebang and
is written for bash. Under dash the whole suite stops there.

## Alpine pass (`inside_alpine.sh` → `alpine_user.sh`, user `user`, shell `/bin/busybox` = ash)

- `sh tests/linux/pty_check.sh`: the same four pty tests passed and did not skip.
- `sh tests/sh/test_install.sh` under ash: `ok 1` to `ok 7` shown, then `skip: curl missing` (the test's own skip,
  it has no curl in the image), `alpine suite passed`.

## Blocked

`PRD LX2 BLOCKED: tests/sh/test_install.sh fails under dash: line 81 uses a bash here-string (<<<), dash: "Syntax
error: redirection unexpected". Fix needs a change to tests/sh/test_install.sh, which LX2 does not own.`

The PRD says to stop and report rather than fix a failure outside the owned files. The Debian pass therefore stops
there. The `tests/linux/**` changes do not hide it: `as_user.sh` runs that check on purpose and the suite stays red.

## What changed in tests/linux

- `run.sh`: runs two images (Debian with `inside.sh`, Alpine with `inside_alpine.sh`). Removes an image only when it
  was not there before the run.
- `pty_check.sh` (new): runs the pty tests and fails if one skips or does not pass.
- `tui_scenario.sh` (new): the TUI in a pty with `script(1)`, the two runs described above.
- `inside_alpine.sh`, `alpine_user.sh` (new): Alpine pass.
- `as_user.sh`: adds the pty check, the TUI scenario and install.sh under dash.
