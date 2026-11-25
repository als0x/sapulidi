#!/usr/bin/env bash
# scripts/release.sh tests, on temporary copies of the working tree (tracked and new files, not ignored ones) with a
# fake go first on PATH. Nothing in the real repository is touched.
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

# new_copy <name> [stub]: C is a committed copy of the working tree, F a bin dir holding the fake go. With "stub" the
# suite (tests/run.sh) is replaced by a stand-in that logs and can be told to fail.
new_copy() {
	C=$work/$1
	F=$work/$1-bin
	LOG=$work/$1.log
	mkdir -p "$C" "$F"
	(cd "$root" && git ls-files -co --exclude-standard -z | tar -c --null -T -) | tar -x -C "$C"
	cp "$fixture/go" "$F/go-fake"
	# "go" records the -ldflags of every build in a file next to the log, then runs the fake go.
	cat >"$F/go" <<'EOF'
#!/bin/sh
prev=
for a in "$@"; do
	[ "$prev" != -ldflags ] || echo "$a" >>"$FAKE_LOG.ldflags"
	prev=$a
done
exec "${0%/*}/go-fake" "$@"
EOF
	chmod +x "$F/go" "$F/go-fake"
	if [ "${2:-}" = stub ]; then
		cp "$fixture/run.sh" "$C/tests/run.sh"
		chmod +x "$C/tests/run.sh"
	fi
	: >"$LOG"
	git -C "$C" init -q
	git -C "$C" remote add origin https://github.com/acme/widget.git
	git -C "$C" add -A
	git -C "$C" -c user.name=t -c user.email=t@example.invalid -c commit.gpgsign=false -c core.hooksPath=/dev/null commit -q -m copy
}

release() { # args...: runs release.sh in the copy with the fake go first on PATH; extra environment goes before it
	(cd "$C" && PATH="$F:$PATH" FAKE_LOG="$LOG" bash scripts/release.sh "$@") >"$work/out.txt" 2>&1
}
unset SAPULIDI_REPO
# builds_with <owner/repo>: all four builds were told that release repository
builds_with() { test "$(grep -c "internal/version\.Repo=$1\$" "$LOG.ldflags")" = 4; }

# fingerprint of the tracked files release.sh rewrites
sig() { (cd "$C" && cat bin/sapulidi install.sh checksums.txt README.md .claude-plugin/*.json .codex-plugin/plugin.json .cursor-plugin/plugin.json .antigravity-plugin/plugin.json | cksum); }
clean() { test -z "$(git -C "$C" status --porcelain)"; }

# The real compiler, offline: is there a local go that can build the copy without the network?
go_build() { # dir out
	(cd "$1" && env -u GOFLAGS GOTOOLCHAIN=local GOPROXY=off CGO_ENABLED=0 go build -trimpath -buildvcs=false -o "$2" ./cmd/sapulidi) >/dev/null 2>&1
}
have_go=0
new_copy real
if command -v go >/dev/null && go_build "$C" "$work/before"; then have_go=1; fi

# A full run: every build already sees the bumped files.
new_copy ok stub
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
pins=$(grep -o 'v[0-9][0-9.]*/install\.sh' "$C/README.md")
check "README.md has install.sh pins" test -n "$pins"
check "every README.md install.sh pin is bumped" test "$(printf '%s\n' "$pins" | sort -u)" = v9.8.7/install.sh
check "the printed commit includes README.md" grep -q '^  git add .*README\.md' "$work/out.txt"
check "the printed plan bumps before it builds" test "$(grep -n 'set SAPULIDI_VERSION' "$work/out.txt" | head -n 1 | cut -d : -f 1)" -lt "$(grep -n 'build into dist' "$work/out.txt" | head -n 1 | cut -d : -f 1)"

check "every build names the repository of the origin remote" builds_with acme/widget

# The built binary reports it: the real compiler with the ldflags release.sh passed, for this machine.
if [ "$have_go" = 1 ]; then
	ldflags=$(head -n 1 "$LOG.ldflags")
	(cd "$C" && env -u GOFLAGS GOTOOLCHAIN=local GOPROXY=off CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$work/repobin" ./cmd/sapulidi) >/dev/null 2>&1
	report=$(env -i HOME="$work/home" "$work/repobin" version --json 2>&1)
	check "the built binary reports the repository" test "$report" = '{"repo":"acme/widget","version":"9.8.7"}'
else
	echo "skip: the local go cannot build offline (GOTOOLCHAIN=local, GOPROXY=off); the reported repository was not checked"
fi

# The other forms of a GitHub remote.
for url in git@github.com:acme/widget.git ssh://git@github.com/acme/widget https://github.com/acme/widget; do
	new_copy remote stub
	git -C "$C" remote set-url origin "$url"
	release 9.8.7; rc=$?
	check "origin $url: exits 0" test "$rc" = 0
	check "origin $url: names acme/widget" builds_with acme/widget
	rm -rf "$C" "$F" "$LOG.ldflags"
done

new_copy envrepo stub
(cd "$C" && PATH="$F:$PATH" FAKE_LOG="$LOG" SAPULIDI_REPO=other/fork bash scripts/release.sh 9.8.7) >"$work/out.txt" 2>&1; rc=$?
check "SAPULIDI_REPO: exits 0" test "$rc" = 0
check "SAPULIDI_REPO wins over the remote" builds_with other/fork

new_copy envonly stub
git -C "$C" remote remove origin
(cd "$C" && PATH="$F:$PATH" FAKE_LOG="$LOG" SAPULIDI_REPO=other/fork bash scripts/release.sh 9.8.7) >"$work/out.txt" 2>&1; rc=$?
check "SAPULIDI_REPO without a remote: exits 0" test "$rc" = 0
check "SAPULIDI_REPO without a remote: used" builds_with other/fork

# No usable repository: refused before anything is touched.
refused() { # label
	check "$1: exit 1" test "$rc" = 1
	check "$1: nothing built" test "$(grep -c '^build' "$LOG")" = 0
	check "$1: the files are as they were" test "$(sig)" = "$before"
	check "$1: the refusal names SAPULIDI_REPO" grep -q 'SAPULIDI_REPO' "$work/out.txt"
}
new_copy norepo stub
git -C "$C" remote remove origin
before=$(sig)
release 9.8.7; rc=$?
refused "no remote and no SAPULIDI_REPO"

new_copy otherhost stub
git -C "$C" remote set-url origin https://gitlab.example.invalid/acme/widget.git
before=$(sig)
release 9.8.7; rc=$?
refused "a remote that is not on GitHub"

new_copy badenv stub
before=$(sig)
(cd "$C" && PATH="$F:$PATH" FAKE_LOG="$LOG" SAPULIDI_REPO='not a repo' bash scripts/release.sh 9.8.7) >"$work/out.txt" 2>&1; rc=$?
refused "a malformed SAPULIDI_REPO"

# The real suites that the stub stood in for must pass on the bumped tree, or a release could never finish.
(cd "$C" && bash tests/sh/test_install.sh) >"$work/bumped-install.txt" 2>&1; rc=$?
check "the real test_install.sh passes after the bump" test "$rc" = 0
[ "$rc" = 0 ] || grep 'not ok' "$work/bumped-install.txt" >&2
(cd "$C" && bash tests/sh/test_launcher.sh) >"$work/bumped-launcher.txt" 2>&1; rc=$?
check "the real test_launcher.sh passes after the bump" test "$rc" = 0
[ "$rc" = 0 ] || grep 'not ok' "$work/bumped-launcher.txt" >&2
if [ "$have_go" = 1 ]; then
	(cd "$C" && env -u GOFLAGS GOTOOLCHAIN=local GOPROXY=off go test ./tests -run 'TestManifest|TestInstallScript|TestReleaseScript') >"$work/bumped-go.txt" 2>&1; rc=$?
	check "the manifest and pin tests pass after the bump" test "$rc" = 0
else
	echo "skip: the local go cannot build offline (GOTOOLCHAIN=local, GOPROXY=off); the Go tests were not run on the bumped copy"
fi

# A failing suite, after checksums.txt was written: everything is put back.
new_copy suitefail stub
before=$(sig)
(cd "$C" && PATH="$F:$PATH" FAKE_LOG="$LOG" FAKE_SUITE_FAIL=1 bash scripts/release.sh 9.8.7) >"$work/out.txt" 2>&1; rc=$?
check "a failing suite exits 1" test "$rc" = 1
check "the suite had the new checksums.txt when it failed" grep -q '^suite checksum_lines=4 ' "$LOG"
check "after a failing suite the tree is clean" clean
check "after a failing suite the files are as they were" test "$(sig)" = "$before"
check "after a failing suite dist is removed" test ! -e "$C/dist"

# A failing build (the third of four): everything is put back, no checksums.txt, no suite.
new_copy buildfail stub
before=$(sig)
(cd "$C" && PATH="$F:$PATH" FAKE_LOG="$LOG" FAKE_FAIL_BUILD=3 bash scripts/release.sh 9.8.7) >"$work/out.txt" 2>&1; rc=$?
check "a failing build exits 1" test "$rc" = 1
check "two builds ran before the failure" test "$(grep -c '^build' "$LOG")" = 2
check "the suite did not run after a failing build" test "$(grep -c '^suite' "$LOG")" = 0
check "after a failing build the tree is clean" clean
check "after a failing build the files are as they were" test "$(sig)" = "$before"
check "after a failing build dist is removed" test ! -e "$C/dist"

# A start with a modified file, an untracked file or an existing dist/ is refused and nothing is touched.
new_copy dirty stub
echo "# edit" >>"$C/install.sh"
before=$(sig)
release 9.8.7; rc=$?
check "a modified file is refused with exit 1" test "$rc" = 1
check "a modified file: nothing built" test "$(grep -c '^build' "$LOG")" = 0
check "a modified file is left as it was" test "$(sig)" = "$before"
check "a modified file: the refusal says why" grep -q 'not clean' "$work/out.txt"

new_copy untracked stub
echo mine >"$C/stray.txt"
before=$(sig)
release 9.8.7; rc=$?
check "an untracked file is refused with exit 1" test "$rc" = 1
check "an untracked file: nothing built" test "$(grep -c '^build' "$LOG")" = 0
check "an untracked file is left as it was" test "$(cat "$C/stray.txt")" = mine -a "$(sig)" = "$before"

new_copy hasdist stub
mkdir "$C/dist"
echo mine >"$C/dist/keep.txt"
before=$(sig)
release 9.8.7; rc=$?
check "an existing dist/ is refused with exit 1" test "$rc" = 1
check "an existing dist/ is not deleted" test "$(cat "$C/dist/keep.txt")" = mine
check "an existing dist/: nothing built" test "$(grep -c '^build' "$LOG")" = 0
check "an existing dist/: the files are as they were" test "$(sig)" = "$before"

# The reason for the order: the binary embeds the manifests, so a version bump changes its bytes.
if [ "$have_go" = 1 ]; then
	# Whatever version the tree carries, "-probe" is appended, so the change is real on a clean and on a bumped tree.
	probe() { # dir label
		go_build "$1" "$work/$2-before"
		sed 's/"version": "\([^"]*\)"/"version": "\1-probe"/' "$1/.claude-plugin/plugin.json" >"$work/plugin-$2.json"
		cat "$work/plugin-$2.json" >"$1/.claude-plugin/plugin.json"
		go_build "$1" "$work/$2-after"
		check "a plugin.json version change changes the binary ($2 tree)" test "$(cksum <"$work/$2-before")" != "$(cksum <"$work/$2-after")"
	}
	probe "$work/real" clean
	# the "ok" copy was bumped by release.sh above: the same check on a tree that already carries the new version
	cp -R "$work/ok" "$work/bumped"
	probe "$work/bumped" bumped
else
	echo "skip: the local go cannot build offline (GOTOOLCHAIN=local, GOPROXY=off); the reproducibility check was not run"
fi

echo "$n checks, $fails failed"
[ "$fails" = 0 ]
