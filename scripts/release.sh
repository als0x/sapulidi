#!/usr/bin/env bash
# Prepare a release. Run by the owner, never by CI.
# usage: scripts/release.sh <version> [--dry-run]
# Builds the four binaries the same way the release workflow does, bumps the version in the launcher,
# install.sh and every manifest, writes checksums.txt, and only then runs tests/run.sh. It prints the commit,
# tag and push commands; it never commits, tags or pushes. The tree must be clean at the start. If any step fails, including the
# suite, a trap runs git checkout on exactly the files it changed (bin/sapulidi, install.sh, checksums.txt and the
# manifests) and removes dist/, so the tree is as it was found.
set -eu
cd "$(dirname "$0")/.."

usage() {
	echo "usage: scripts/release.sh <version> [--dry-run]   (version like 0.1.0 or 0.1.0-rc1, no leading v)" >&2
	exit 2
}

version_re='^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'

version=
dry=0
for a in "$@"; do
	case $a in
	--dry-run) dry=1 ;;
	-*) usage ;;
	*)
		[ -z "$version" ] || usage
		version=$a
		;;
	esac
done
[ -n "$version" ] || usage
if ! [[ $version =~ $version_re ]]; then
	usage
fi

manifests=".claude-plugin/plugin.json .claude-plugin/marketplace.json .codex-plugin/plugin.json .cursor-plugin/plugin.json .antigravity-plugin/plugin.json"
changed="bin/sapulidi install.sh checksums.txt $manifests"
targets="darwin/arm64 darwin/amd64 linux/arm64 linux/amd64"
mod=github.com/als0x/sapulidi

# Every build runs with this environment, so it matches the release workflow.
gobuild() { # os arch out
	env -u GOFLAGS -u GOARM64 CGO_ENABLED=0 GOAMD64=v1 GOTOOLCHAIN=local GOOS="$1" GOARCH="$2" \
		go build -trimpath -buildvcs=false \
		-ldflags "-s -w -X $mod/internal/version.Version=$version" -o "$3" ./cmd/sapulidi
}

echo "release plan for v$version:"
echo "  0. check the tree is clean and go matches the go.mod toolchain line"
echo "  1. build into dist/ (CGO_ENABLED=0, GOAMD64=v1, -trimpath -buildvcs=false):"
for t in $targets; do
	echo "       sapulidi_${version}_${t%/*}_${t#*/}"
done
echo "  2. set SAPULIDI_VERSION=$version in bin/sapulidi and install.sh and write checksums.txt"
for f in $manifests; do
	echo "  3. set \"version\": \"$version\" in $f"
done
echo "  4. check install.sh and bin/sapulidi pin the same version"
echo "  5. run bash tests/run.sh (after the new checksums.txt is written)"
echo "  6. print the commit, tag and push commands (not run)"

dirty=$(git status --porcelain)
want=$(sed -n 's/^toolchain go//p' go.mod)
got=$(env -u GOFLAGS GOTOOLCHAIN=local go env GOVERSION)

if [ "$dry" = 1 ]; then
	[ -z "$dirty" ] || echo "warning: the working tree is not clean; a real run would refuse"
	[ "go$want" = "$got" ] || echo "warning: go is $got, go.mod toolchain line wants go$want; a real run would fail"
	echo "dry run: no file was changed"
	exit 0
fi

if [ -n "$dirty" ]; then
	echo "the working tree is not clean; commit or stash first" >&2
	exit 1
fi
if [ "go$want" != "$got" ]; then
	echo "go is $got, go.mod toolchain line wants go$want; install it (checksums depend on it)" >&2
	exit 1
fi

restore() {
	status=$?
	if [ "$status" -ne 0 ]; then
		# The tree was clean at the start, so putting the tracked files back is safe.
		# shellcheck disable=SC2086
		git checkout -- $changed 2>/dev/null || true
		rm -rf dist
		echo "release failed; files restored" >&2
	fi
}
trap restore EXIT

rm -rf dist
mkdir dist
for t in $targets; do
	gobuild "${t%/*}" "${t#*/}" "dist/sapulidi_${version}_${t%/*}_${t#*/}"
done

replace() { # file sed-expression
	sed "$2" "$1" >"$1.tmp"
	cat "$1.tmp" >"$1"
	rm -f "$1.tmp"
}

replace bin/sapulidi "s/^SAPULIDI_VERSION=.*/SAPULIDI_VERSION=$version/"
replace install.sh "s/^SAPULIDI_VERSION=.*/SAPULIDI_VERSION=$version/"
for f in $manifests; do
	replace "$f" "s/\"version\": \"[^\"]*\"/\"version\": \"$version\"/"
done
if command -v sha256sum >/dev/null 2>&1; then
	(cd dist && sha256sum sapulidi_*) >checksums.txt
else
	(cd dist && shasum -a 256 sapulidi_*) >checksums.txt
fi

# install.sh and the launcher must pin the same version.
pin_launcher=$(sed -n 's/^SAPULIDI_VERSION=//p' bin/sapulidi)
pin_install=$(sed -n 's/^SAPULIDI_VERSION=//p' install.sh)
if [ "$pin_launcher" != "$version" ] || [ "$pin_install" != "$version" ]; then
	echo "bin/sapulidi pins $pin_launcher and install.sh pins $pin_install, want $version" >&2
	exit 1
fi

# The suite runs last, against the new checksums.txt and the bumped files, so main is never red after the commit.
bash tests/run.sh

echo
echo "ready. Review the diff, then run:"
echo "  git add $changed"
echo "  git commit -m \"Release v$version\""
echo "  git tag v$version"
echo "  git push origin HEAD v$version"
echo "If the release job later reports different checksums, see CONTRIBUTING.md."
