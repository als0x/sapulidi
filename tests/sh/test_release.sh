#!/usr/bin/env bash
# scripts/release.sh tests, on a temporary copy of the repository with a fake go first on PATH. The copy is made
# from HEAD plus the working-tree release.sh, so nothing in the real repository is touched.
set -u
root=$(cd "$(dirname "$0")/../.." && pwd)
fixture=$root/tests/sh/fixtures/release
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

command -v git >/dev/null || { echo "skip: git missing"; exit 0; }

# new_copy <name>: C is a committed copy of the repository with a stub suite, F is a bin dir holding the fake go
new_copy() {
	C=$work/$1
	F=$work/$1-bin
	LOG=$work/$1.log
	mkdir -p "$C" "$F"
	git -C "$root" archive HEAD | tar -x -C "$C"
	cp "$root/scripts/release.sh" "$C/scripts/release.sh"
	cp "$fixture/run.sh" "$C/tests/run.sh"
	cp "$fixture/go" "$F/go"
	chmod +x "$F/go" "$C/tests/run.sh"
	: >"$LOG"
	git -C "$C" init -q
	git -C "$C" add -A
	git -C "$C" -c user.name=t -c user.email=t@example.invalid commit -q -m copy
}

release() { # args...: runs release.sh in the copy with the fake go first on PATH
	(cd "$C" && PATH="$F:$PATH" FAKE_LOG="$LOG" bash scripts/release.sh "$@") >"$work/out.txt" 2>&1
}

new_copy ok
release 9.8.7; rc=$?
check "release exits 0" test "$rc" = 0
check "four builds" test "$(grep -c '^build' "$LOG")" = 4
check "every build sees the bumped launcher" test "$(grep -c '^build launcher=9.8.7 ' "$LOG")" = 4
check "every build sees the bumped manifest" test "$(grep -c ' manifest=9.8.7 ' "$LOG")" = 4
check "checksums.txt is the same during all builds" test "$(grep '^build' "$LOG" | sed 's/.*checksums=//' | sort -u | wc -l | tr -d ' ')" = 1
check "checksums.txt is rewritten after the last build" test "$(grep '^build' "$LOG" | tail -n 1 | sed 's/.*checksums=//')" != "$(tail -n 1 "$LOG" | sed 's/.*checksums=//')"
check "checksums.txt is complete when the suite runs" grep -q '^suite checksum_lines=4 ' "$LOG"
check "the suite runs after the last build" test "$(tail -n 1 "$LOG" | cut -d ' ' -f 1)" = suite
check "the suite runs once" test "$(grep -c '^suite' "$LOG")" = 1
check "install.sh is bumped" grep -q '^SAPULIDI_VERSION=9.8.7$' "$C/install.sh"
check "the printed plan bumps before it builds" test "$(grep -n 'set SAPULIDI_VERSION' "$work/out.txt" | head -n 1 | cut -d : -f 1)" -lt "$(grep -n 'build into dist' "$work/out.txt" | head -n 1 | cut -d : -f 1)"

new_copy fail
(cd "$C" && PATH="$F:$PATH" FAKE_LOG="$LOG" FAKE_SUITE_FAIL=1 bash scripts/release.sh 9.8.7) >"$work/out.txt" 2>&1; rc=$?
check "a failing suite fails the release" test "$rc" = 1
check "the bumped files are restored" test -z "$(git -C "$C" status --porcelain)"
check "dist is removed" test ! -e "$C/dist"

# The reason for the order: the binary embeds the manifests, so a version bump changes its bytes.
if command -v go >/dev/null; then
	new_copy real
	build() { (cd "$C" && env -u GOFLAGS CGO_ENABLED=0 go build -trimpath -buildvcs=false -o "$1" ./cmd/sapulidi) >/dev/null 2>&1; }
	if build "$work/before"; then
		sed 's/"version": "[^"]*"/"version": "9.8.7"/' "$C/.claude-plugin/plugin.json" >"$work/plugin.json"
		cat "$work/plugin.json" >"$C/.claude-plugin/plugin.json"
		build "$work/after"
		check "a plugin.json version change changes the binary" test "$(cksum <"$work/before")" != "$(cksum <"$work/after")"
	else
		echo "skip: go build failed in the copy"
	fi
else
	echo "skip: go missing"
fi

echo "$n checks, $fails failed"
[ "$fails" = 0 ]
