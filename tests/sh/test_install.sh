#!/usr/bin/env bash
# install.sh tests: a fixture release directory and a raw directory served through file://, a fake uname and id,
# and a private PATH.
set -u
root=$(cd "$(dirname "$0")/../.." && pwd)
fixture=$root/tests/sh/fixtures/install
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

sha() { if command -v sha256sum >/dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d ' ' -f 1; }

# new_env <name> [uname_s] [uname_m] [uid]: sets T (test dir), P (private PATH), S (script), REL, RAW, H (home)
new_env() {
	local name=$1 us=${2:-Linux} um=${3:-x86_64} uid=${4:-1000}
	T=$work/$name
	H=$T/home
	mkdir -p "$T/path" "$T/rel" "$T/raw" "$H"
	P=$T/path
	REL=$T/rel
	RAW=$T/raw
	S=$T/install.sh
	cp "$root/install.sh" "$S"
	printf '#!/bin/sh\nif [ "$1" = -s ]; then echo %s; else echo %s; fi\n' "$us" "$um" >"$P/uname"
	printf '#!/bin/sh\necho %s\n' "$uid" >"$P/id"
	chmod +x "$P/uname" "$P/id"
	local t
	for t in sh env dirname rmdir mkdir rm mv chmod ln ls grep sort tail awk cut find sleep cat date mktemp readlink; do
		command -v "$t" >/dev/null && [ ! -e "$P/$t" ] && ln -s "$(command -v "$t")" "$P/$t"
	done
	for t in sha256sum shasum curl; do
		command -v "$t" >/dev/null && ln -s "$(command -v "$t")" "$P/$t"
	done
	printf 'export ZSH=1\n' >"$H/.zshrc"
	printf 'export BASH=1\n' >"$H/.bashrc"
	return 0
}

# publish <asset-suffix>: the fake binary goes to REL, the matching checksums.txt to RAW, a decoy to REL
publish() {
	local asset=sapulidi_0.1.0_$1
	cp "$fixture/sapulidi" "$REL/$asset"
	printf '%s  %s\n' "$(sha "$REL/$asset")" "$asset" >"$RAW/checksums.txt"
	printf '%s  %s\n' "0000000000000000000000000000000000000000000000000000000000000000" "$asset" >"$REL/checksums.txt"
}

run() { # runs install.sh with the private PATH
	env -i PATH="$P" HOME="$H" SAPULIDI_DATA="$T/data" SAPULIDI_RELEASE_BASE="file://$REL" SAPULIDI_RAW_BASE="file://$RAW" "$@"
}

rcsum() { cat "$H/.zshrc" "$H/.bashrc" | { if command -v sha256sum >/dev/null; then sha256sum; else shasum -a 256; fi; }; }
nothing_left() { [ ! -e "$T/data/bin/0.1.0" ] && [ ! -e "$T/data/bin/current" ] && [ ! -e "$H/.local/bin/sapulidi" ]; }

command -v curl >/dev/null || { echo "skip: curl missing"; exit 0; }
check "sh -n" sh -n "$root/install.sh"
if command -v shellcheck >/dev/null; then check "shellcheck" shellcheck -s sh "$root/install.sh"; fi
check "at most 150 lines" test "$(wc -l <"$root/install.sh" | tr -d ' ')" -le 150
check "pinned values" grep -q '^SAPULIDI_VERSION=0.1.0$' "$root/install.sh"

# install, verify, link; rc files untouched
new_env t1
publish linux_amd64
before=$(rcsum)
out=$(run sh "$S" 2>&1); rc=$?
check "installs" test "$rc" = 0 -a -x "$T/data/bin/0.1.0/sapulidi"
check "current symlink" test "$(readlink "$T/data/bin/current")" = 0.1.0
check "launcher-style link" test "$(readlink "$H/.local/bin/sapulidi")" = "$T/data/bin/current/sapulidi"
check "linked binary runs" test "$("$H/.local/bin/sapulidi" a)" = "fake sapulidi a"
check "prints where and version" grep -q "$T/data/bin/0.1.0/sapulidi" <<<"$out"
check "prints version" grep -q '0\.1\.0' <<<"$out"
check "prints the PATH line" grep -q 'export PATH=.*\.local/bin' <<<"$out"
check "no rc file touched" test "$(rcsum)" = "$before"
check "no temp or lock left" test -z "$(ls -A "$T/data/bin" | grep -v -e '^0.1.0$' -e '^current$')" -a -z "$(ls -A "$T/data/bin/0.1.0" | grep -v '^sapulidi$')"

# second run: already installed, nothing downloaded
rm -rf "$REL"/* "$RAW"/*
out=$(run sh "$S" 2>&1); rc=$?
check "second run exits 0" test "$rc" = 0
check "second run says already installed" grep -q 'already installed' <<<"$out"

# PATH line is not printed when the directory is on PATH
new_env t1b
publish linux_amd64
out=$(env -i PATH="$P:$H/.local/bin" HOME="$H" SAPULIDI_DATA="$T/data" SAPULIDI_RELEASE_BASE="file://$REL" SAPULIDI_RAW_BASE="file://$RAW" sh "$S" 2>&1)
check "no PATH line when on PATH" test -z "$(grep 'export PATH' <<<"$out")"

# dry run
new_env t2
publish linux_amd64
out=$(run sh "$S" --dry-run 2>&1); rc=$?
check "dry-run exits 0" test "$rc" = 0
check "dry-run changes nothing" test ! -e "$T/data" -a ! -e "$H/.local"
check "dry-run prints each step" test "$(grep -c -e download -e checksums.txt -e verify -e link <<<"$out")" -ge 3

# checksum mismatch
new_env t3
publish linux_amd64
echo tampered >>"$REL/sapulidi_0.1.0_linux_amd64"
out=$(run sh "$S" 2>&1); rc=$?
check "mismatch exits 3" test "$rc" = 3
check "mismatch message" grep -q 'checksum mismatch' <<<"$out"
check "mismatch installs nothing" nothing_left

# missing checksum line
new_env t4
publish linux_amd64
: >"$RAW/checksums.txt"
out=$(run sh "$S" 2>&1); rc=$?
check "missing checksum line exits 3" test "$rc" = 3
check "missing checksum installs nothing" nothing_left

# checksums come from the raw base: the decoy in the release directory is ignored
new_env t5
publish linux_amd64
rm "$RAW/checksums.txt"
out=$(run sh "$S" 2>&1); rc=$?
check "release-dir checksums.txt is not used" test "$rc" = 3
check "no raw checksums installs nothing" nothing_left

# download failure
new_env t6
publish linux_amd64
rm "$REL/sapulidi_0.1.0_linux_amd64"
out=$(run sh "$S" 2>&1); rc=$?
check "failed download exits 3" test "$rc" = 3
check "failed download installs nothing" nothing_left

# http bases refused
new_env t7
publish linux_amd64
out=$(run env SAPULIDI_RELEASE_BASE=http://example.invalid/x sh "$S" 2>&1); rc=$?
check "http release base exits 3" test "$rc" = 3
out=$(run env SAPULIDI_RAW_BASE=http://example.invalid/x sh "$S" 2>&1); rc=$?
check "http raw base exits 3" test "$rc" = 3
check "http base installs nothing" nothing_left

# no curl and no wget
new_env t8
publish linux_amd64
rm -f "$P/curl"
out=$(run sh "$S" 2>&1); rc=$?
check "no tool exits 3" test "$rc" = 3
check "no tool installs nothing" nothing_left

# root is refused
new_env t9 Linux x86_64 0
publish linux_amd64
out=$(run sh "$S" 2>&1); rc=$?
check "root exits 1 with one line" test "$rc" = 1 -a "$(wc -l <<<"$out" | tr -d ' ')" = 1
check "root installs nothing" nothing_left

# a foreign file or a foreign link at ~/.local/bin/sapulidi is refused
new_env t10
publish linux_amd64
mkdir -p "$H/.local/bin"
echo mine >"$H/.local/bin/sapulidi"
out=$(run sh "$S" 2>&1); rc=$?
check "foreign file refused" test "$rc" = 1 -a "$(cat "$H/.local/bin/sapulidi")" = mine
check "foreign file: no binary installed" test ! -e "$T/data/bin/0.1.0"
rm "$H/.local/bin/sapulidi"
ln -s /nonexistent/elsewhere "$H/.local/bin/sapulidi"
out=$(run sh "$S" 2>&1); rc=$?
check "foreign link refused" test "$rc" = 1 -a "$(readlink "$H/.local/bin/sapulidi")" = /nonexistent/elsewhere
# a link into the data dir is replaced
rm "$H/.local/bin/sapulidi"
ln -s "$T/data/bin/0.0.1/sapulidi" "$H/.local/bin/sapulidi"
out=$(run sh "$S" 2>&1); rc=$?
check "own link is replaced" test "$rc" = 0 -a "$(readlink "$H/.local/bin/sapulidi")" = "$T/data/bin/current/sapulidi"

# passthrough after --
new_env t11
publish linux_amd64
out=$(run sh "$S" -- --agents claude --schedule 30m 2>&1); rc=$?
check "passthrough exits 0" test "$rc" = 0
check "passthrough runs the binary with the arguments" grep -q '^fake sapulidi --agents claude --schedule 30m$' <<<"$out"
new_env t12
publish linux_amd64
out=$(run sh "$S" -- --bogus 2>&1); rc=$?
check "unknown flag exits 2" test "$rc" = 2
check "unknown flag is reported" grep -q 'unknown flag' <<<"$out"
check "unknown flag still installed" test -x "$T/data/bin/0.1.0/sapulidi"

# bin/current moves forward only
new_env t13
publish linux_amd64
mkdir -p "$T/data/bin/0.2.0"
cp "$fixture/sapulidi" "$T/data/bin/0.2.0/sapulidi"; chmod +x "$T/data/bin/0.2.0/sapulidi"
run sh "$S" >/dev/null 2>&1
check "current is not moved back" test "$(readlink "$T/data/bin/current")" = 0.2.0

# uname mapping
new_env t14 Darwin arm64
publish darwin_arm64
out=$(run sh "$S" 2>&1); rc=$?
check "darwin arm64 installs" test "$rc" = 0 -a -x "$T/data/bin/0.1.0/sapulidi"

echo "$n checks, $fails failed"
[ "$fails" = 0 ]
