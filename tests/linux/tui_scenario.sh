#!/usr/bin/env bash
# The TUI in a real pseudo-terminal, as an unprivileged user, inside the Debian container (run by as_user.sh).
# The binary runs under script(1) on a 100x30 pty; keys come through a fifo, and every step waits for the screen text
# it needs. Run 1: q on the places list with one stale go-build folder; the folder stays and the terminal is back in
# cooked mode (stty -a shows icanon and echo). Run 2: enter to open the place, d, y, q, q; only that folder goes, the
# sentinel file stays.
# SAPULIDI_TEST=1 with SAPULIDI_TEST_NO_CTIME=1 lets the planted mtimes decide age; the test hooks point the temp
# category and the agent scratch dir into the sandbox.
set -eu
cd /work

W=/home/user/tui-scenario
rm -rf "$W"
mkdir -p "$W/h" "$W/tmp" "$W/scratch" "$W/bin"
export HOME="$W/h" TMPDIR="$W/tmp" SAPULIDI_TEST=1 SAPULIDI_TEST_NO_CTIME=1
export SAPULIDI_TEST_TEMPDIR="$W/tmp" SAPULIDI_TEST_SCRATCH="$W/scratch"
export XDG_CONFIG_HOME="$W/h/c" XDG_DATA_HOME="$W/h/d" XDG_STATE_HOME="$W/h/s" TERM=xterm
BIN="$W/bin/sapulidi"
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o "$BIN" ./cmd/sapulidi

# One stale leftover (60 days old) and a sentinel outside the temp dir that nothing may touch.
victim="$W/tmp/go-build-stale"
mkdir -p "$victim/obj"
echo stale >"$victim/obj/a.o"
find "$victim" -exec touch -h -d "60 days ago" {} +
echo keep >"$W/h/keep.txt"

keys="$W/keys"
trap 'kill $(jobs -p) 2>/dev/null || true' EXIT
fail=0
check() { # check <description> <command...>
	local desc=$1
	shift
	if "$@"; then
		echo "ok: $desc"
	else
		echo "FAIL: $desc" >&2
		fail=1
	fi
}

# start <typescript>: runs the binary on a pty in the background; fd 3 is the writer of the key fifo.
start() {
	rm -f "$keys"
	mkfifo "$keys"
	local cmd="stty rows 30 cols 100; \"$BIN\"; rc=\$?; stty -a; exit \$rc"
	timeout 120 script -qfc "$cmd" "$1" <"$keys" >/dev/null 2>&1 &
	pid=$!
	exec 3>"$keys"
}

wait_for() { # wait_for <typescript> <text>: up to 60 s for the text to show up on the screen
	local i
	for i in $(seq 600); do
		if grep -q -- "$2" "$1" 2>/dev/null; then
			return 0
		fi
		sleep 0.1
	done
	echo "FAIL: '$2' never appeared in $1" >&2
	return 1
}

# Run 1: quit with q; the leftover must stay and the terminal must be cooked again.
ts1="$W/run1.typescript"
start "$ts1"
wait_for "$ts1" "$W/tmp/"
printf 'q' >&3
status1=0
wait "$pid" || status1=$?
exec 3>&-
check "q leaves the stale folder in place" test -f "$victim/obj/a.o"
check "q exits with status 0" test "$status1" -eq 0
check "after q the terminal is in cooked mode (icanon)" grep -q ' icanon ' "$ts1"
check "after q the terminal echoes (echo)" grep -q ' echo ' "$ts1"

# Run 2: open the place, delete the preselected folder with d and y, then leave the result screen and quit.
ts2="$W/run2.typescript"
start "$ts2"
wait_for "$ts2" "$W/tmp/"
printf '\r' >&3
wait_for "$ts2" go-build-stale
printf 'd' >&3
wait_for "$ts2" "Delete 1 item"
printf 'y' >&3
wait_for "$ts2" "removed 1 items"
printf 'q' >&3
printf 'q' >&3
status2=0
wait "$pid" || status2=$?
exec 3>&-
check "d and y remove the stale folder" test ! -e "$victim"
check "the sentinel file stays" test -f "$W/h/keep.txt"
check "q exits with status 0 after the delete" test "$status2" -eq 0
check "after the delete the terminal is in cooked mode (icanon)" grep -q ' icanon ' "$ts2"

if [ "$fail" -ne 0 ]; then
	echo "tui scenario FAILED" >&2
	exit 1
fi
echo "tui scenario passed"
