# Linux container suite: result (PRD LX2)

Command: `bash tests/linux/run.sh` on the owner's Podman (rootless, arm64 host, Podman 5.6.2).
Overall: **passed** (`linux suite passed`, exit 0). One leg is **skipped**, not passed: install.sh under BusyBox ash.

## Images

| Image | Size pulled | Existed before the run | Result |
|---|---|---|---|
| `docker.io/library/golang:1.26` (Debian, `/bin/sh` = dash) | 916,243,449 bytes | no | passed |
| `docker.io/library/golang:1.26-alpine` (BusyBox, `/bin/sh` = ash) | 249,961,448 bytes | no | pty tests passed; install.sh **skipped** |

Both images were removed afterwards with `podman rmi`: the run script does this for any image that was not present
before the run. The other images in the owner's Podman were not touched.

## Debian pass (`inside.sh` → `as_user.sh`, user `user`, `/bin/sh` = dash)

- `bash tests/run.sh`: all checks passed (gofmt, vet for linux and darwin, `go test ./...`, the `tests/sh` suites:
  20 codex hook checks, 29 hook checks, 74 install checks with 0 failed, 36 launcher checks with 0 failed).
- `sh tests/linux/pty_check.sh` (`go test -v ./internal/tui/... ./cmd/sapulidi/...`): passed and not skipped:
  `TestPTYEnterRestore`, `TestPTYKeysAndSize`, `TestPTYSignalsRestoreAndExit`, `TestTUIQuitsAndLeavesCookedMode`.
- `bash tests/linux/scenario.sh`: `scenario passed` (old go-build and old node_modules removed; young ones kept; cron
  entry installed; doctor ran).
- `bash tests/linux/tui_scenario.sh`: `tui scenario passed`
  - run 1: `q` leaves the stale folder in place, exit 0, `stty -a` shows `icanon` and `echo`;
  - run 2: space, `d`, `y`, `q`, `q`: the stale folder is removed, `keep.txt` stays, exit 0, `icanon` back.
- `bash tests/sh/test_install.sh`: 74 checks, 0 failed. The harness is bash by design and runs `install.sh` with `sh`,
  which is dash in this image, so install.sh was exercised under dash.

## Alpine pass (`inside_alpine.sh` → `alpine_user.sh`, user `user`, `/bin/sh` = BusyBox ash)

- `sh tests/linux/pty_check.sh`: the same four pty tests passed and were not skipped.
- install.sh: **skipped**. The harness needs bash and this image has none. Its own guard also skips when curl is
  missing (`skip: curl missing`, exit 0), and this image has no curl. Nothing was installed in the container to work
  around it. **install.sh was therefore not exercised under BusyBox ash in this run.**

## Changes in tests/linux

- `run.sh`: runs both images (Debian with `inside.sh`, Alpine with `inside_alpine.sh`). Removes an image only when it
  was not there before the run.
- `pty_check.sh` (new): runs the pty tests and fails if one skips or does not pass.
- `tui_scenario.sh` (new): the TUI in a pty with `script(1)`, the two runs described above.
- `inside_alpine.sh`, `alpine_user.sh` (new): the Alpine pass.
- `as_user.sh`: adds the pty check, the TUI scenario, and `bash tests/sh/test_install.sh`.
