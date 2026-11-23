#!/bin/sh
# Codex session-start hook (untested against a real Codex, SP3): run `sapulidi check` and print its lines.
# Same silence rule as the Claude Code hook: the launcher (which may download) never runs in the foreground;
# when the binary is not installed yet the download starts in the background and the hook says so once.
# The plugin root is PLUGIN_ROOT when the host sets it (that name is UNVERIFIED for Codex), else the directory
# above this script, so the working directory of the session never matters. Always exits 0.
root=${PLUGIN_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}
launcher=$root/bin/sapulidi

if [ -n "${SAPULIDI_STATE:-}" ]; then
	state=$SAPULIDI_STATE
elif [ -n "${XDG_STATE_HOME:-}" ]; then
	state=$XDG_STATE_HOME/sapulidi
else
	state=${HOME:-/nonexistent}/.local/state/sapulidi
fi
if [ -n "${SAPULIDI_DATA:-}" ]; then
	data=$SAPULIDI_DATA
elif [ -n "${XDG_DATA_HOME:-}" ]; then
	data=$XDG_DATA_HOME/sapulidi
else
	data=${HOME:-/nonexistent}/.local/share/sapulidi
fi
marker=$state/hook-unavailable-noted

unavailable() { # the one line, first time only
	if [ ! -e "$marker" ]; then
		echo "sapulidi is not available yet (it needs one download); it stays quiet until it works"
		mkdir -p "$state" 2>/dev/null && : >"$marker" 2>/dev/null
	fi
}

if [ ! -x "$launcher" ]; then
	unavailable
	exit 0
fi

version=$(sed -n '/^SAPULIDI_VERSION=/{s/^[^=]*=//p;q;}' "$launcher" 2>/dev/null)

if [ -z "$version" ] || [ ! -x "$data/bin/$version/sapulidi" ]; then
	nohup "$launcher" --sapulidi-path >/dev/null 2>&1 </dev/null &
	unavailable
	exit 0
fi

out=$("$launcher" check --host codex --host-version "$version" 2>&1 </dev/null)
case $? in
0) [ -z "$out" ] || printf '%s\n' "$out" ;;
3 | 126 | 127) unavailable ;;
*) [ -z "$out" ] || printf '%s\n' "$out" ;;
esac
exit 0
