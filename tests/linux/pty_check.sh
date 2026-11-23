#!/bin/sh
# Runs the pseudo-terminal tests of the TUI and the CLI, and fails when one of them skips or does not pass: a skip
# would hide a broken terminal path. POSIX sh, so it runs under BusyBox ash and dash. Run inside the containers.
set -eu
log=$(mktemp)
if ! go test -count=1 -v ./internal/tui/... ./cmd/sapulidi/... >"$log" 2>&1; then
	tail -40 "$log"
	echo "FAIL: go test of the TUI and the CLI" >&2
	exit 1
fi
for name in TestPTYEnterRestore TestPTYKeysAndSize TestPTYSignalsRestoreAndExit TestTUIQuitsAndLeavesCookedMode; do
	if grep -q -- "--- SKIP: $name " "$log"; then
		echo "FAIL: $name skipped" >&2
		exit 1
	fi
	if ! grep -q -- "--- PASS: $name " "$log"; then
		echo "FAIL: $name did not pass" >&2
		exit 1
	fi
	echo "ok: $name passed, not skipped"
done
rm -f "$log"
