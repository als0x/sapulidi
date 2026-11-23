#!/usr/bin/env bash
# Claude Code hook tests: a fake plugin root with a stub launcher, a private state dir, no real sapulidi.
set -u
root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fails=0
n=0

ok() { n=$((n + 1)); echo "ok $n - $1"; }
bad() { n=$((n + 1)); fails=$((fails + 1)); echo "not ok $n - $1" >&2; }
check() { # name, command...
	local name=$1
	shift
	if "$@"; then ok "$name"; else bad "$name"; fi
}

# new_plugin <name> <stub body>: sets P (plugin root), S (state dir), LOG (file the stub appends its argv to)
new_plugin() {
	P=$work/$1/plugin
	S=$work/$1/state
	LOG=$work/$1/calls
	D=
	mkdir -p "$P/hooks" "$P/bin" "$S"
	cp "$root/hooks/claude-session-start.sh" "$root/hooks/claude-session-end.sh" "$P/hooks/"
	{
		echo '#!/bin/sh'
		echo 'SAPULIDI_VERSION=7.7.7'
		echo "echo \"\$*\" >>'$LOG'"
		echo "$2"
	} >"$P/bin/sapulidi"
	chmod +x "$P/bin/sapulidi" "$P/hooks/claude-session-start.sh" "$P/hooks/claude-session-end.sh"
}

hook() { # script: uses P (plugin root), S (state dir) and D (data dir)
	env -i PATH="$PATH" HOME="$work/home" CLAUDE_PLUGIN_ROOT="$P" SAPULIDI_STATE="$S" SAPULIDI_DATA="${D:-$work/nodata}" "$P/hooks/$1"
}

# install_bin: pretend the launcher already downloaded version 7.7.7, so the start hook may run check
install_bin() {
	D=$(dirname "$P")/data
	mkdir -p "$D/bin/7.7.7"
	printf '#!/bin/sh\nexit 0\n' >"$D/bin/7.7.7/sapulidi"
	chmod +x "$D/bin/7.7.7/sapulidi"
}

waitfor() { # file, pattern: poll every 0.1 s, up to 10 s, for a line
	local i=0
	while [ "$i" -lt 100 ]; do
		grep -qx -- "$2" "$1" 2>/dev/null && return 0
		sleep 0.1
		i=$((i + 1))
	done
	return 1
}

check "sh -n start" sh -n "$root/hooks/claude-session-start.sh"
check "sh -n end" sh -n "$root/hooks/claude-session-end.sh"
if command -v shellcheck >/dev/null; then
	check "shellcheck start" shellcheck -s sh "$root/hooks/claude-session-start.sh"
	check "shellcheck end" shellcheck -s sh "$root/hooks/claude-session-end.sh"
fi
check "hooks.json names both scripts" sh -c "grep -q 'SessionStart' '$root/hooks/hooks.json' && grep -q 'SessionEnd' '$root/hooks/hooks.json' && grep -q 'CLAUDE_PLUGIN_ROOT}/hooks/claude-session-start.sh' '$root/hooks/hooks.json' && grep -q 'CLAUDE_PLUGIN_ROOT}/hooks/claude-session-end.sh' '$root/hooks/hooks.json'"
check "hooks.json gives both hooks a 15 s timeout" test "$(grep -c '"timeout": 15' "$root/hooks/hooks.json")" = 2

# the start script prints the check line and passes host and version
new_plugin t1 'echo "sapulidi is installed but not set up: run /sapulidi setup"'
install_bin
out=$(hook claude-session-start.sh)
status=$?
check "start exits 0" test "$status" = 0
check "start prints the check line" test "$out" = "sapulidi is installed but not set up: run /sapulidi setup"
check "start passes host and the pinned version" grep -qx 'check --host claude-code --host-version 7.7.7' "$LOG"

# nothing to say: no output at all
new_plugin t2 'exit 0'
install_bin
out=$(hook claude-session-start.sh)
check "start is silent when check is silent" test -z "$out"

# the end script returns at once and the background run still happens
new_plugin t3 'sleep 10'
D=
t0=$(date +%s)
hook claude-session-end.sh
status=$?
t1=$(date +%s)
check "end exits 0" test "$status" = 0
check "end returns without waiting for the slow run" test $((t1 - t0)) -lt 5
waited=0
while [ ! -s "$LOG" ] && [ "$waited" -lt 100 ]; do
	sleep 0.1
	waited=$((waited + 1))
done
check "end started the quick run in the background" grep -qx 'run --quick --trigger claude-code' "$LOG"

# no binary yet: the start hook never runs the launcher in the foreground (it may download); it starts the
# download in the background, says so once, then stays silent; always exit 0 and fast
new_plugin t4 'sleep 10; echo "sapulidi: download failed" >&2; exit 3'
D=
t0=$(date +%s)
out=$(hook claude-session-start.sh)
status=$?
t1=$(date +%s)
check "no-binary start exits 0" test "$status" = 0
check "no-binary start does not wait for the slow download" test $((t1 - t0)) -lt 5
check "no-binary start prints exactly one line" test "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" = 1
check "no-binary line mentions the download" sh -c "printf '%s' '$out' | grep -q 'one download'"
check "no-binary start began the download in the background" waitfor "$LOG" '--sapulidi-path'
check "no check ran without a binary" sh -c "! grep -q '^check' '$LOG'"
out=$(hook claude-session-start.sh)
status=$?
check "second no-binary start exits 0" test "$status" = 0
check "second no-binary start is silent" test -z "$out"

# the binary is installed but the launcher itself fails (exit 3): one line once, then silence
new_plugin t4b 'echo "sapulidi: download failed" >&2; exit 3'
install_bin
out=$(hook claude-session-start.sh)
check "launcher failure prints one line" test "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" = 1
check "launcher failure line does not leak the error" sh -c "! printf '%s' '$out' | grep -q 'download failed'"
out=$(hook claude-session-start.sh)
check "launcher failure is then silent" test -z "$out"

# a failing check is not a launcher failure: its message is shown, every time, and the marker stays unset
new_plugin t4c 'echo "sapulidi: check: state is unreadable"; exit 1'
install_bin
out=$(hook claude-session-start.sh)
status=$?
check "failing check exits 0 in the hook" test "$status" = 0
check "failing check message is printed" test "$out" = "sapulidi: check: state is unreadable"
out=$(hook claude-session-start.sh)
check "failing check message is printed again" test "$out" = "sapulidi: check: state is unreadable"
check "failing check leaves no unavailable marker" test ! -e "$S/hook-unavailable-noted"

# a missing launcher file: the unavailable line once
new_plugin t4d 'exit 0'
install_bin
rm -f "$P/bin/sapulidi"
out=$(hook claude-session-start.sh)
check "missing launcher prints one line" test "$(printf '%s\n' "$out" | wc -l | tr -d ' ')" = 1
out=$(hook claude-session-start.sh)
check "missing launcher is then silent" test -z "$out"

# a missing launcher behaves the same: the end script exits 0 without output
new_plugin t5 'exit 0'
rm -f "$P/bin/sapulidi"
out=$(hook claude-session-end.sh)
check "end without a launcher is silent and exits 0" test "$?" = 0 -a -z "$out"

if [ "$fails" -ne 0 ]; then
	echo "$fails of $n hook checks failed" >&2
	exit 1
fi
echo "all $n hook checks passed"
