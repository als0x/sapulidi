#!/usr/bin/env bash
# Prepare a release. Run by the owner, never by CI.
# usage: scripts/release.sh <version> [--dry-run]
# The binaries check for updates in one GitHub repository: SAPULIDI_REPO=owner/repo, or the one the origin remote names.
# Bumps the version in the launcher, install.sh and every manifest (the binary embeds them), builds the four
# binaries from that tree the same way the release workflow does, writes checksums.txt, and only then runs tests/run.sh. It prints the commit,
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

# The release repository the binaries are told about: SAPULIDI_REPO, else a GitHub origin remote.
repo=${SAPULIDI_REPO:-}
if [ -z "$repo" ]; then
	url=$(git remote get-url origin 2>/dev/null || true)
	repo=$(printf '%s\n' "$url" | sed -nE 's#^(https://|ssh://git@|git@)github\.com[:/]([^/]+/[^/]+)$#\2#p')
	repo=${repo%.git}
fi
repo_re='^[0-9A-Za-z-]+/[0-9A-Za-z._-]+$'
[[ $repo =~ $repo_re ]] || repo=
no_repo="no release repository: set SAPULIDI_REPO=owner/repo, or leave it unset with a GitHub origin remote"

# Every build runs with this environment, so it matches the release workflow.
gobuild() { # os arch out
	env -u GOFLAGS -u GOARM64 CGO_ENABLED=0 GOAMD64=v1 GOTOOLCHAIN=local GOOS="$1" GOARCH="$2" \
		go build -trimpath -buildvcs=false \
		-ldflags "-s -w -X $mod/internal/version.Version=$version -X $mod/internal/version.Repo=$repo" -o "$3" ./cmd/sapulidi
}

echo "release plan for v$version:"
echo "  0. check the tree is clean, dist/ does not exist and go matches the go.mod toolchain line"
echo "  1. set SAPULIDI_VERSION=$version in bin/sapulidi and install.sh (the binary embeds them)"
for f in $manifests; do
	echo "  2. set \"version\": \"$version\" in $f"
done
echo "  3. check install.sh, bin/sapulidi and the manifests carry the new version"
echo "  4. build into dist/ from the bumped tree (CGO_ENABLED=0, GOAMD64=v1, -trimpath -buildvcs=false)"
echo "     with updates checked in the repository ${repo:-(none)}:"
for t in $targets; do
	echo "       sapulidi_${version}_${t%/*}_${t#*/}"
done
echo "  5. write checksums.txt from dist/"
echo "  6. run bash tests/run.sh (after the new checksums.txt is written)"
echo "  7. print the commit, tag and push commands (not run)"

dirty=$(git status --porcelain)
want=$(sed -n 's/^toolchain go//p' go.mod)
got=$(env -u GOFLAGS GOTOOLCHAIN=local go env GOVERSION)

if [ "$dry" = 1 ]; then
	[ -z "$dirty" ] || echo "warning: the working tree is not clean; a real run would refuse"
	[ ! -e dist ] || echo "warning: dist/ exists; a real run would refuse"
	[ "go$want" = "$got" ] || echo "warning: go is $got, go.mod toolchain line wants go$want; a real run would fail"
	[ -n "$repo" ] || echo "warning: $no_repo; a real run would refuse"
	echo "dry run: no file was changed"
	exit 0
fi

if [ -z "$repo" ]; then
	echo "$no_repo" >&2
	exit 1
fi
if [ -n "$dirty" ]; then
	echo "the working tree is not clean; commit or stash first" >&2
	exit 1
fi
if [ "go$want" != "$got" ]; then
	echo "go is $got, go.mod toolchain line wants go$want; install it (checksums depend on it)" >&2
	exit 1
fi

# dist/ is gitignored, so the clean check above does not see it; never delete something this script did not create.
if [ -e dist ] || [ -L dist ]; then
	echo "dist/ already exists; move or remove it first (release.sh only deletes a dist/ it created)" >&2
	exit 1
fi

restore() {
	status=$?
	if [ "$status" -ne 0 ]; then
		# The tree was clean at the start, so putting the tracked files back is safe.
		for f in $changed; do rm -f "$f.tmp"; done
		# shellcheck disable=SC2086
		if git checkout -- $changed; then
			echo "release failed; files restored" >&2
		else
			echo "release failed and git checkout could not restore $changed; run git status and git checkout -- on them" >&2
		fi
		rm -rf dist
	fi
}
trap restore EXIT

replace() { # file sed-expression
	sed "$2" "$1" >"$1.tmp"
	cat "$1.tmp" >"$1"
	rm -f "$1.tmp"
}

# The binary embeds the manifests, skills, hooks and bin/sapulidi, so every file is bumped before the first build.
replace bin/sapulidi "s/^SAPULIDI_VERSION=.*/SAPULIDI_VERSION=$version/"
replace install.sh "s/^SAPULIDI_VERSION=.*/SAPULIDI_VERSION=$version/"
for f in $manifests; do
	replace "$f" "s/\"version\": \"[^\"]*\"/\"version\": \"$version\"/"
done

# Nothing is built until the launcher, install.sh and every manifest carry the new version.
pin_launcher=$(sed -n 's/^SAPULIDI_VERSION=//p' bin/sapulidi)
pin_install=$(sed -n 's/^SAPULIDI_VERSION=//p' install.sh)
if [ "$pin_launcher" != "$version" ] || [ "$pin_install" != "$version" ]; then
	echo "bin/sapulidi pins $pin_launcher and install.sh pins $pin_install, want $version" >&2
	exit 1
fi
for f in $manifests; do
	if [ "$(grep -c "\"version\": \"$version\"" "$f")" != 1 ]; then
		echo "$f does not carry exactly one \"version\": \"$version\" after the bump" >&2
		exit 1
	fi
done

mkdir dist
for t in $targets; do
	gobuild "${t%/*}" "${t#*/}" "dist/sapulidi_${version}_${t%/*}_${t#*/}"
done
if command -v sha256sum >/dev/null 2>&1; then
	(cd dist && sha256sum sapulidi_*) >checksums.txt
else
	(cd dist && shasum -a 256 sapulidi_*) >checksums.txt
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
