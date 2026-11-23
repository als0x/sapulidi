# sapulidi on Claude Code

Hooks: the plugin runs `sapulidi check` at session start and a quick sweep in the background at session end
(`hooks/hooks.json`). Nothing else is needed for the heartbeat.

- The launcher is on the PATH of Bash commands as `sapulidi` while the plugin is enabled.
- Terminal tier (`procs stop`, adding a user rule, `uninstall --all`): print the exact command for the user to
  type in their own terminal and never run it; never use script, expect, a pty or a pipe to get around it. The `!`
  prefix is not promised to provide a terminal (spike SP7), so do not suggest it for these.
- Long builds or test runs: suggest `/loop 10m sapulidi check` to watch free disk. It is only a suggestion;
  sapulidi installs nothing for it.
- First run: a session start line says sapulidi is not set up; the user answers with `/sapulidi setup`.
