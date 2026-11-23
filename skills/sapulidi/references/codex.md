# sapulidi on Codex

Status: untested. Codex was never run on the author's machine; the hooks follow its public documentation.

Hooks: `hooks/codex-hooks.json` runs `sapulidi check --host codex` at session start and a background
`sapulidi run --quick --trigger codex` at the end of every turn (the `Stop` event is per turn, not per session;
the engine's run lock makes an overlapping run exit as busy). The name `PLUGIN_ROOT` for the plugin directory is
UNVERIFIED; the scripts fall back to their own directory. If the hooks did not load, run
`sapulidi check --host codex --host-version <v>` whenever this skill is used, so the heartbeat still arrives.

- Terminal tier (`procs stop`, adding a user rule, `uninstall --all`): print the exact command for the user to
  type in their own terminal and never run it; never use script, expect, a pty or a pipe to get around it.
- If the session start check is missing, `sapulidi schedule install` covers the cleaning anyway.
