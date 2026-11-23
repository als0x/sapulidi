#!/usr/bin/env bash
# Runs as the unprivileged user inside the container (started by inside.sh): the repository suite, the pty tests,
# the end-to-end scenario, the TUI scenario in a pty, and install.sh under /bin/sh (dash on Debian).
set -eu
export HOME=/home/user
export GOPATH=$HOME/go GOMODCACHE=$HOME/go/pkg/mod GOCACHE=$HOME/.cache/go-build
cd /work
bash tests/run.sh
sh tests/linux/pty_check.sh
bash tests/linux/scenario.sh
bash tests/linux/tui_scenario.sh
echo "shell: $(readlink -f /bin/sh)"
# The harness is bash by design; it runs install.sh itself with sh, which is dash here.
bash tests/sh/test_install.sh
