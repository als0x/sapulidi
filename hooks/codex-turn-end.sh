#!/bin/sh
# Codex turn-end hook (untested against a real Codex, SP3). Codex's Stop event fires at the end of EVERY turn,
# not once per session. Start a quick sweep in the background and return at once; the engine's run lock makes
# an overlapping run exit as busy, so frequent turns do not pile up sweeps. Its output goes nowhere: the run
# writes its own log and summary into the state directory.
# The plugin root is PLUGIN_ROOT when set (that name is UNVERIFIED for Codex), else the directory above this script.
root=${PLUGIN_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}
launcher=$root/bin/sapulidi

[ -x "$launcher" ] || exit 0
nohup "$launcher" run --quick --trigger codex >/dev/null 2>&1 </dev/null &
exit 0
