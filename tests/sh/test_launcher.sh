#!/usr/bin/env bash
# Launcher tests: a fixture release directory served through file://, a fake uname and a private PATH.
set -u
root=$(cd "$(dirname "$0")/../.." && pwd)
fixture=$root/tests/sh/fixtures/launcher
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

# The pinned version comes from the launcher, so the suite also passes after scripts/release.sh bumped it.
# N1 and N2 are arbitrary versions above any pin, for the "current moves forward" checks.
V=$(sed -n 's/^SAPULIDI_VERSION=//p' "$root/bin/sapulidi")
N1=98.2.0 N2=98.10.0

sha() { if command -v sha256sum >/dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d ' ' -f 1; }

# new_env <name> [uname_s] [uname_m] [tools...]: sets T (test dir), P (private PATH), L (launcher), REL (release dir)
new_env() {
	local name=$1 us=${2:-Linux} um=${3:-x86_64}
	T=$work/$name
	mkdir -p "$T/path" "$T/rel" "$T/launcher/bin" "$T/data"
	P=$T/path
	REL=$T/rel
	L=$T/launcher/bin/sapulidi
	cp "$root/bin/sapulidi" "$L"
	printf '#!/bin/sh\nif [ "$1" = -s ]; then echo %s; else echo %s; fi\n' "$us" "$um" >"$P/uname"
	chmod +x "$P/uname"
	local t
	for t in sh env dirname rmdir mkdir rm mv chmod ln ls grep sort tail awk cut find sleep cat date; do
		command -v "$t" >/dev/null && [ ! -e "$P/$t" ] && ln -s "$(command -v "$t")" "$P/$t"
	done
	for t in sha256sum shasum; do
		command -v "$t" >/dev/null && ln -s "$(command -v "$t")" "$P/$t"
	done
	command -v curl >/dev/null && ln -s "$(command -v curl)" "$P/curl"
	return 0
}

# publish <asset-suffix>: put the fake binary in REL and write a matching checksums.txt next to the launcher
publish() {
	local asset=sapulidi_${V}_$1
	cp "$fixture/sapulidi" "$REL/$asset"
	printf '%s  %s\n' "$(sha "$REL/$asset")" "$asset" >"$T/launcher/checksums.txt"
}

run() { # runs the launcher with the private PATH
	env -i PATH="$P" HOME="$T/home" SAPULIDI_DATA="$T/data" SAPULIDI_RELEASE_BASE="file://$REL" "$@"
}

command -v curl >/dev/null || { echo "skip: curl missing"; exit 0; }
check "sh -n" sh -n "$root/bin/sapulidi"
if command -v shellcheck >/dev/null; then check "shellcheck" shellcheck -s sh "$root/bin/sapulidi"; fi
check "checksums.txt has one SHA-256 line per target" test "$(grep -c "^[0-9a-f]\{64\}  sapulidi_${V//./\\.}_\(darwin\|linux\)_\(arm64\|amd64\)\$" "$root/checksums.txt")" = 4

# download, verify, install, exec
new_env t1
publish linux_amd64
out=$(run sh "$L" a "b c"); rc=$?
check "downloads and execs" test "$rc" = 0 -a "$out" = "fake sapulidi a b c"
check "installed 0755" test -x "$T/data/bin/$V/sapulidi"
check "current symlink" test "$(readlink "$T/data/bin/current")" = "$V"
check "no temp or lock left" test -z "$(ls -A "$T/data/bin/$V" | grep -v '^sapulidi$')" -a ! -d "$T/data/bin/.lock"

# second call does not download
rm -rf "$REL"/*
out=$(run sh "$L" again); rc=$?
check "second call needs no download" test "$rc" = 0 -a "$out" = "fake sapulidi again"

# --sapulidi-path
out=$(run sh "$L" --sapulidi-path)
check "--sapulidi-path" test "$out" = "$T/data/bin/$V/sapulidi"

# --sapulidi-path still downloads when missing
new_env t1b
publish linux_amd64
out=$(run sh "$L" --sapulidi-path)
check "--sapulidi-path downloads when missing" test "$out" = "$T/data/bin/$V/sapulidi" -a -x "$T/data/bin/$V/sapulidi"

# checksum mismatch
new_env t2
publish linux_amd64
echo tampered >>"$REL/sapulidi_${V}_linux_amd64"
out=$(run sh "$L" 2>&1); rc=$?
check "mismatch exits 3" test "$rc" = 3
check "mismatch message" grep -q 'checksum mismatch' <<<"$out"
check "mismatch leaves no file" test -z "$(ls -A "$T/data/bin/$V" 2>/dev/null)"

# missing checksum line
new_env t3
publish linux_amd64
: >"$T/launcher/checksums.txt"
out=$(run sh "$L" 2>&1); rc=$?
check "missing checksum line exits 3" test "$rc" = 3
check "missing checksum leaves no file" test -z "$(ls -A "$T/data/bin/$V" 2>/dev/null)"

# download failure
new_env t3b
publish linux_amd64
rm "$REL"/*
out=$(run sh "$L" 2>&1); rc=$?
check "failed download exits 3" test "$rc" = 3
check "failed download message" grep -q 'sapulidi: download failed' <<<"$out"

# no curl and no wget
new_env t4
publish linux_amd64
rm -f "$P/curl"
out=$(run sh "$L" 2>&1); rc=$?
check "no tool exits 3" test "$rc" = 3
check "no tool prints manual command" grep -q "need curl or wget.*sapulidi_${V}_linux_amd64" <<<"$out"

# uname mapping and unknown OS
new_env t5 Linux aarch64
publish linux_arm64
out=$(run sh "$L" x); rc=$?
check "aarch64 maps to linux_arm64" test "$rc" = 0 -a "$out" = "fake sapulidi x"
new_env t5b Darwin arm64
publish darwin_arm64
out=$(run sh "$L" x); rc=$?
check "darwin arm64" test "$rc" = 0 -a "$out" = "fake sapulidi x"
new_env t6 FreeBSD x86_64
out=$(run sh "$L" 2>&1); rc=$?
check "unknown OS exits 1" test "$rc" = 1

# bin/current moves forward only
new_env t7
publish linux_amd64
mkdir -p "$T/data/bin/$N1"
cp "$fixture/sapulidi" "$T/data/bin/$N1/sapulidi"; chmod +x "$T/data/bin/$N1/sapulidi"
ln -s "$V" "$T/data/bin/current"
mkdir -p "$T/data/bin/$V"; cp "$fixture/sapulidi" "$T/data/bin/$V/sapulidi"; chmod +x "$T/data/bin/$V/sapulidi"
run sh "$L" >/dev/null
check "current moves $V -> $N1" test "$(readlink "$T/data/bin/current")" = "$N1"
mkdir -p "$T/data/bin/$N2"; cp "$fixture/sapulidi" "$T/data/bin/$N2/sapulidi"; chmod +x "$T/data/bin/$N2/sapulidi"
run sh "$L" >/dev/null
check "versions compare numerically ($N2 > $N1)" test "$(readlink "$T/data/bin/current")" = "$N2"
rm -rf "$T/data/bin/$N2"
run sh "$L" >/dev/null
check "current is not moved back" test "$(readlink "$T/data/bin/current")" = "$N1"

# concurrent start: one download
new_env t8
publish linux_amd64
real=$(command -v curl)
rm -f "$P/curl"
printf '#!/bin/sh\necho x >>"%s/downloads"\nsleep 2\nexec "%s" "$@"\n' "$T" "$real" >"$P/curl"
chmod +x "$P/curl"
rm -f "$T/downloads"
run sh "$L" one >"$T/o1" 2>&1 &
p1=$!
run sh "$L" two >"$T/o2" 2>&1 &
p2=$!
wait $p1; r1=$?
wait $p2; r2=$?
check "concurrent: both succeed" test "$r1" = 0 -a "$r2" = 0 -a "$(cat "$T/o1")" = "fake sapulidi one" -a "$(cat "$T/o2")" = "fake sapulidi two"
check "concurrent: one download" test "$(wc -l <"$T/downloads" | tr -d ' ')" = 1

# stale lock (older than 10 minutes) is taken over
new_env t9
publish linux_amd64
mkdir -p "$T/data/bin/.lock"
touch -t 202001010000 "$T/data/bin/.lock"
out=$(run sh "$L" s); rc=$?
check "stale lock taken over" test "$rc" = 0 -a "$out" = "fake sapulidi s"

# stale lock that cannot be removed: gives up within the wait limit
new_env t10
publish linux_amd64
mkdir -p "$T/data/bin/.lock"
touch "$T/data/bin/.lock/held"
touch -t 202001010000 "$T/data/bin/.lock"
start=$(date +%s)
out=$(run env SAPULIDI_LOCK_WAIT=2 sh "$L" 2>&1); rc=$?
check "unremovable stale lock exits 3 within the wait limit" test "$rc" = 3 -a $(($(date +%s) - start)) -lt 10
check "unremovable stale lock message" grep -q 'timed out waiting' <<<"$out"

# plain http base is refused
new_env t11
publish linux_amd64
out=$(run env SAPULIDI_RELEASE_BASE=http://example.invalid/x sh "$L" 2>&1); rc=$?
check "http base refused with exit 3" test "$rc" = 3
check "http base message" grep -q 'release base must be https' <<<"$out"

# umask 000: data directories are not group or world writable
new_env t12
publish linux_amd64
run env SAPULIDI_DATA="$T/fresh/data" sh -c 'umask 000; exec sh "$0"' "$L" >/dev/null 2>&1
perms_ok=1
for d in "$T/fresh/data" "$T/fresh/data/bin" "$T/fresh/data/bin/$V"; do
	m=$(ls -ld "$d" | cut -c1-10)
	case $m in d????-??-?) ;; *) perms_ok=0 ;; esac
done
check "umask 000 leaves data dirs not group/world writable" test "$perms_ok" = 1 -a -x "$T/fresh/data/bin/$V/sapulidi"

# a planted symlink at the temp name is not written through
new_env t13
publish linux_amd64
mkdir -p "$T/data/bin/$V"
echo keep >"$T/victim"
ln -s "$T/victim" "$T/data/bin/$V/.download"
out=$(run sh "$L" p); rc=$?
check "planted temp symlink not written through" test "$rc" = 0 -a "$(cat "$T/victim")" = keep -a "$out" = "fake sapulidi p"

# a symlinked version directory is refused
new_env t14
publish linux_amd64
mkdir -p "$T/data/bin" "$T/elsewhere"
ln -s "$T/elsewhere" "$T/data/bin/$V"
out=$(run sh "$L" 2>&1); rc=$?
check "symlinked version dir refused" test "$rc" = 3 -a -z "$(ls -A "$T/elsewhere")"

# empty HOME and relative data dir
new_env t15
publish linux_amd64
out=$(env -i PATH="$P" HOME= SAPULIDI_RELEASE_BASE="file://$REL" sh "$L" 2>&1); rc=$?
check "empty HOME exits 1" test "$rc" = 1
out=$(run env SAPULIDI_DATA=rel/data sh "$L" 2>&1); rc=$?
check "relative SAPULIDI_DATA exits 1" test "$rc" = 1

# the install route is recorded in the state directory after a verified install
new_env t16
publish linux_amd64
run sh "$L" >/dev/null
default_state=.local/state/sapulidi # under the HOME that run() sets
H=$T/home
check "marker in the default state dir" test -f "$H/$default_state/installed-by/plugin"
check "state dir is private" test "$(ls -ld "$H/$default_state" | cut -c1-10)" = drwx------
new_env t16b
publish linux_amd64
run env XDG_STATE_HOME="$T/xdg" sh "$L" >/dev/null
check "marker under XDG_STATE_HOME" test -f "$T/xdg/sapulidi/installed-by/plugin"
new_env t16c
publish linux_amd64
mkdir -p "$T/state/installed-by"
echo keep >"$T/victim"
ln -s "$T/victim" "$T/state/installed-by/plugin"
run env SAPULIDI_STATE="$T/state" XDG_STATE_HOME="$T/xdg" sh "$L" >/dev/null
check "marker under SAPULIDI_STATE, not through a planted symlink" \
	test -f "$T/state/installed-by/plugin" -a ! -L "$T/state/installed-by/plugin" -a "$(cat "$T/victim")" = keep -a ! -e "$T/xdg"
new_env t16d
publish linux_amd64
echo tampered >>"$REL/sapulidi_${V}_linux_amd64"
run sh "$L" >/dev/null 2>&1
check "no marker after a failed install" test ! -e "$T/home/$default_state/installed-by/plugin"
new_env t16e
publish linux_amd64
mkdir -p "$T/state"
chmod 500 "$T/state"
out=$(run env SAPULIDI_STATE="$T/state" sh "$L" ro 2>"$T/err"); rc=$?
chmod 700 "$T/state"
check "read-only state dir does not fail the install" test "$rc" = 0 -a "$out" = "fake sapulidi ro"
if [ "$(id -u)" != 0 ]; then
	check "read-only state dir is reported" grep -q 'could not record the install route' "$T/err"
fi

echo "$n checks, $fails failed"
[ "$fails" = 0 ]
