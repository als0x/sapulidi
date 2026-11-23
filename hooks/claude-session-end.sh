#!/bin/sh
# Claude Code SessionEnd hook: start a quick sweep in the background and return at once. The background
# process survives the session (spike SP3). Its output goes nowhere: the run writes its own log and summary
# into the state directory. A failing launcher (offline) ends that process quietly.
root=${CLAUDE_PLUGIN_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}
launcher=$root/bin/sapulidi

[ -x "$launcher" ] || exit 0
nohup "$launcher" run --quick --trigger claude-code >/dev/null 2>&1 </dev/null &
exit 0
