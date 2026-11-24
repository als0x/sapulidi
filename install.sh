#!/bin/sh
# sapulidi installer: fetch the pinned binary, verify it, install it, link it. Usage: install.sh [--dry-run] [-- args]
# The whole body is one function called on the last line, so a download cut off in the middle runs nothing.
SAPULIDI_VERSION=0.1.0
SAPULIDI_REPO=als0x/sapulidi

main() {
	say() { echo "sapulidi: $*"; }
	die() {
		code=$1
		shift
		echo "sapulidi: $*" >&2
		exit "$code"
	}

	dry=0
	while [ $# -gt 0 ]; do
		case $1 in
		--dry-run) dry=1 ;;
		--) shift; break ;;
		*) break ;; # sh -s -- args drops the "--": the first other argument starts the arguments for sapulidi
		esac
		shift
	done

	uid=$(id -u 2>/dev/null)
	{ [ -n "$uid" ] && [ "$uid" != 0 ]; } || die 1 "refusing to run as root or without a user id; run it as your own user"
	case "$(uname -s)" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) die 1 "unsupported OS: $(uname -s)" ;;
	esac
	case "$(uname -m)" in
	arm64 | aarch64) arch=arm64 ;;
	x86_64 | amd64) arch=amd64 ;;
	*) die 1 "unsupported architecture: $(uname -m)" ;;
	esac
	[ -n "${HOME:-}" ] || die 1 "HOME is empty"

	asset="sapulidi_${SAPULIDI_VERSION}_${os}_${arch}"
	data=${SAPULIDI_DATA:-${XDG_DATA_HOME:-$HOME/.local/share}/sapulidi}
	case $data in
	/*) ;;
	*) die 1 "data directory must be an absolute path: $data" ;;
	esac
	bindir=$data/bin
	dir=$bindir/$SAPULIDI_VERSION
	bin=$dir/sapulidi
	link=$HOME/.local/bin/sapulidi
	base=${SAPULIDI_RELEASE_BASE:-https://github.com/$SAPULIDI_REPO/releases/download/v$SAPULIDI_VERSION}
	raw=${SAPULIDI_RAW_BASE:-https://raw.githubusercontent.com/$SAPULIDI_REPO/v$SAPULIDI_VERSION}
	for b in "release:$base" "raw:$raw"; do
		case ${b#*:} in
		https://* | file://*) ;;
		*) die 3 "${b%%:*} base must be https://: ${b#*:}" ;;
		esac
	done
	installed() { [ -f "$bin" ] && [ -x "$bin" ]; }

	if [ -e "$link" ] || [ -L "$link" ]; then
		case $(readlink "$link" 2>/dev/null) in
		*/../* | */..) die 1 "$link has a link target with '..'; move it away and run again" ;;
		"$data"/*) ;;
		*) die 1 "$link exists and is not a link into $data; move it away and run again" ;;
		esac
	fi
	if ! installed; then
		command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 || die 3 "need curl or wget"
		command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1 || die 3 "need sha256sum or shasum"
	fi

	if installed; then
		say "$SAPULIDI_VERSION is already installed at $bin; nothing to download"
	else
		say "will download $base/$asset"
		say "will fetch $raw/checksums.txt, verify the SHA-256 and install it as $bin"
	fi
	say "will point $bindir/current at the highest installed version and link $link to it"
	[ "$dry" = 0 ] || { say "dry run: nothing was changed"; exit 0; }

	tmp= locked=0 lock=$bindir/.lock
	cleanup() {
		[ -z "$tmp" ] || rm -rf "$tmp"
		[ "$locked" != 1 ] || rmdir "$lock" 2>/dev/null
	}
	trap cleanup EXIT
	trap 'exit 3' HUP INT TERM

	fetch() { # url dest
		if command -v curl >/dev/null 2>&1; then
			curl -fsSL --proto '=https,file' --proto-redir '=https' --connect-timeout 15 --max-time 300 -o "$2" "$1"
		else
			# BusyBox wget has no --https-only, so wget cannot refuse an https to http redirect; curl is preferred.
			wget -qO "$2" -T 30 "$1"
		fi
	}

	sha256() {
		if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d ' ' -f 1; else shasum -a 256 "$1" | cut -d ' ' -f 1; fi
	}

	install_binary() {
		tmp=$(mktemp -d "$bindir/.install.XXXXXX") || die 3 "cannot create a temp directory in $bindir"
		fetch "$raw/checksums.txt" "$tmp/checksums.txt" || die 3 "download failed: $raw/checksums.txt"
		want=$(awk -v a="$asset" '{ sub(/\r$/, "") } $2 == a { print $1 }' "$tmp/checksums.txt" | sort -u)
		[ -n "$want" ] || die 3 "no checksum line for $asset in $raw/checksums.txt"
		[ "$(echo "$want" | grep -c .)" = 1 ] || die 3 "conflicting checksum lines for $asset in $raw/checksums.txt"
		fetch "$base/$asset" "$tmp/$asset" || die 3 "download failed: $base/$asset"
		[ "$(sha256 "$tmp/$asset")" = "$want" ] || die 3 "checksum mismatch for $asset"
		chmod 0755 "$tmp/$asset" && mkdir -p "$dir" && mv -f "$tmp/$asset" "$bin" || die 3 "cannot install $bin"
	}

	# Same rules as bin/sapulidi: current only moves forward (numeric per dot part) and is replaced by one rename.
	update_current() {
		top=$(ls "$bindir" 2>/dev/null | grep -E '^[0-9]+(\.[0-9]+)*$' | sort -t . -k1,1n -k2,2n -k3,3n -k4,4n | tail -n 1)
		[ -f "$bindir/$top/sapulidi" ] || return 0
		new=$bindir/.current.$$
		rm -f "$new"
		if ln -s "$top" "$new" 2>/dev/null; then
			mv -fh "$new" "$bindir/current" 2>/dev/null || mv -fT "$new" "$bindir/current" 2>/dev/null ||
				ln -sfn "$top" "$bindir/current" 2>/dev/null
			rm -f "$new"
		fi
	}

	oldumask=$(umask)
	umask 077
	if ! installed; then
		[ ! -L "$bindir" ] && [ ! -L "$dir" ] || die 3 "refusing symlinked directory under $data"
		mkdir -p "$bindir" || die 3 "cannot create $bindir"
		waited=0
		while :; do
			if mkdir "$lock" 2>/dev/null; then
				locked=1
				break
			fi
			installed && break
			if [ -n "$(find "$lock" -maxdepth 0 -mmin +10 2>/dev/null)" ] && rmdir "$lock" 2>/dev/null; then continue; fi
			sleep 1
			waited=$((waited + 1))
			[ "$waited" -le "${SAPULIDI_LOCK_WAIT:-700}" ] || die 3 "timed out waiting for $lock"
		done
		installed || install_binary
		if [ "$locked" = 1 ]; then rmdir "$lock" 2>/dev/null; locked=0; fi
	fi
	update_current
	umask "$oldumask"
	cur=$(readlink "$bindir/current" 2>/dev/null)
	[ -f "$bindir/current/sapulidi" ] && [ -x "$bindir/current/sapulidi" ] || die 3 "$bindir/current/sapulidi is not an executable file"
	[ "$cur" = "$SAPULIDI_VERSION" ] || say "current points at $cur, newer than $SAPULIDI_VERSION; $cur will run"
	mkdir -p "$HOME/.local/bin" && rm -f "$link" && ln -s "$bindir/current/sapulidi" "$link" || die 1 "cannot link $link"

	say "installed $bin (version $SAPULIDI_VERSION), linked as $link"
	case ":${PATH:-}:" in
	*":$HOME/.local/bin:"*) ;;
	*) say "add $HOME/.local/bin to your PATH, for example:"; echo "export PATH=\"\$HOME/.local/bin:\$PATH\"" ;;
	esac

	if [ $# -gt 0 ]; then
		run=$bindir/current/sapulidi
		if [ -t 0 ]; then "$run" "$@"; else "$run" "$@" </dev/null; fi
		rc=$?
		[ "$rc" != 2 ] || echo "sapulidi: the installed binary rejected these arguments (exit 2)" >&2
		exit "$rc"
	fi
}

main "$@"
