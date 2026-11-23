# sapulidi on Antigravity

Status: untested. Antigravity has no session hooks for this plugin, so the session start check is not available.

- Whenever this skill is used, first run `sapulidi check --host antigravity --host-version <v>` (`<v>` is the
  `version` in `.antigravity-plugin/plugin.json`). Without a heartbeat for 30 days the schedule becomes a dry run.
- To get cleaning without hooks, offer `sapulidi schedule install` (chat tier: only after a yes).
- Terminal tier (`procs stop`, adding a user rule, `uninstall --all`): print the exact command for the user to
  type in their own terminal and never run it; never use script, expect, a pty or a pipe to get around it.
