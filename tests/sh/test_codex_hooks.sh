#!/usr/bin/env bash
# Codex hook tests: a fake plugin root with a stub launcher, a private state dir, no real sapulidi.
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
	cp "$root/hooks/codex-session-start.sh" "$root/hooks/codex-turn-end.sh" "$P/hooks/"
	{
		echo '#!/bin/sh'
		echo 'SAPULIDI_VERSION=7.7.7'
		echo "echo \"\$*\" >>'$LOG'"
		echo "$2"
	} >"$P/bin/sapulidi"
	chmod +x "$P/bin/sapulidi" "$P/hooks/codex-session-start.sh" "$P/hooks/codex-turn-end.sh"
}

hook() { # script: uses P, S and D; Codex gives the plugin root as PLUGIN_ROOT
	env -i PATH="$PATH" HOME="$work/home" PLUGIN_ROOT="$P" SAPULIDI_STATE="$S" SAPULIDI_DATA="${D:-$work/nodata}" "$P/hooks/$1"
}

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

check "sh -n start" sh -n "$root/hooks/codex-session-start.sh"
check "sh -n end" sh -n "$root/hooks/codex-turn-end.sh"
if command -v shellcheck >/dev/null; then
	check "shellcheck start" shellcheck -s sh "$root/hooks/codex-session-start.sh"
	check "shellcheck end" shellcheck -s sh "$root/hooks/codex-turn-end.sh"
fi
check "codex-hooks.json is valid JSON" sh -c "python3 -c 'import json,sys; json.load(open(sys.argv[1]))' '$root/hooks/codex-hooks.json' 2>/dev/null || ! command -v python3 >/dev/null"
check "codex-hooks.json names both scripts under PLUGIN_ROOT" sh -c "grep -q 'SessionStart' '$root/hooks/codex-hooks.json' && grep -q 'Stop' '$root/hooks/codex-hooks.json' && grep -q 'PLUGIN_ROOT}/hooks/codex-session-start.sh' '$root/hooks/codex-hooks.json' && grep -q 'PLUGIN_ROOT}/hooks/codex-turn-end.sh' '$root/hooks/codex-hooks.json'"

# start prints the check line and passes the codex host and the pinned version
new_plugin t1 'echo "sapulidi is installed but not set up: run /sapulidi setup"'
install_bin
out=$(hook codex-session-start.sh)
check "start exits 0" test "$?" = 0
check "start prints the check line" test "$out" = "sapulidi is installed but not set up: run /sapulidi setup"
check "start passes host codex and the pinned version" grep -qx 'check --host codex --host-version 7.7.7' "$LOG"

# the plugin root does not depend on the working directory: run from elsewhere, with and without PLUGIN_ROOT
new_plugin t1b 'echo "from elsewhere"'
install_bin
out=$(cd "$work" && hook codex-session-start.sh)
check "start works from another working directory" test "$out" = "from elsewhere"
out=$(cd "$work" && env -i PATH="$PATH" HOME="$work/home" SAPULIDI_STATE="$S" SAPULIDI_DATA="$D" "$P/hooks/codex-session-start.sh")
check "start falls back to the script directory without PLUGIN_ROOT" test "$out" = "from elsewhere"

# nothing to say: silent
new_plugin t2 'exit 0'
install_bin
out=$(hook codex-session-start.sh)
check "start is silent when check is silent" test -z "$out"

# end returns at once; the quick run happens in the background with the codex trigger
new_plugin t3 'sleep 10'
t0=$(date +%s)
hook codex-turn-end.sh
status=$?
t1=$(date +%s)
check "end exits 0" test "$status" = 0
check "end returns without waiting for the slow run" test $((t1 - t0)) -lt 5
check "end started the quick run in the background" waitfor "$LOG" 'run --quick --trigger codex'

# no binary yet: the download starts in the background, one line once, then silence
new_plugin t4 'sleep 10; exit 3'
t0=$(date +%s)
out=$(hook codex-session-start.sh)
status=$?
t1=$(date +%s)
check "no-binary start exits 0" test "$status" = 0
check "no-binary start does not wait for the slow download" test $((t1 - t0)) -lt 5
check "no-binary start prints one line about the download" sh -c "test \"\$(printf '%s\n' '$out' | wc -l | tr -d ' ')\" = 1 && printf '%s' '$out' | grep -q 'one download'"
check "no-binary start began the download" waitfor "$LOG" '--sapulidi-path'
out=$(hook codex-session-start.sh)
check "second no-binary start is silent" test -z "$out"

# failing check: message shown, exit 0
new_plugin t4c 'echo "sapulidi: check: state is unreadable"; exit 1'
install_bin
out=$(hook codex-session-start.sh)
check "failing check exits 0 and prints its message" test "$?" = 0 -a "$out" = "sapulidi: check: state is unreadable"

# missing launcher: end is silent and exits 0
new_plugin t5 'exit 0'
rm -f "$P/bin/sapulidi"
out=$(hook codex-turn-end.sh)
check "end without a launcher is silent and exits 0" test "$?" = 0 -a -z "$out"

if [ "$fails" -ne 0 ]; then
	echo "$fails of $n codex hook checks failed" >&2
	exit 1
fi
echo "all $n codex hook checks passed"
