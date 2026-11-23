# sapulidi on Pi

Status: untested. Pi has no session hooks here, so the session start check is not available.

- Run the launcher by path: `<skill dir>/../../bin/sapulidi` (see `.pi/INSTALL.md`), or as `sapulidi` when it is
  on the PATH.
- Whenever this skill is used, first run `sapulidi check --host pi --host-version <v>` (`<v>` is the `version`
  in `.claude-plugin/plugin.json`). Without a heartbeat for 30 days the schedule becomes a dry run.
- To get cleaning without hooks, offer `sapulidi schedule install` (chat tier: only after a yes).
- Terminal tier (`procs stop`, adding a user rule, `uninstall --all`): print the exact command for the user to
  type in their own terminal and never run it; never use script, expect, a pty or a pipe to get around it.
