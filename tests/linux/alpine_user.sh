#!/bin/sh
# Runs as the unprivileged user inside the Alpine container (started by inside_alpine.sh): the pty tests, then
# install.sh under the image's /bin/sh, which is BusyBox ash.
set -eu
export HOME=/home/user GOPATH=/home/user/go GOMODCACHE=/home/user/go/pkg/mod GOCACHE=/home/user/.cache/go-build
export PATH=/go/bin:/usr/local/go/bin:/usr/bin:/bin CGO_ENABLED=0
cd /work
echo "shell: $(readlink -f /bin/sh)"
sh tests/linux/pty_check.sh
sh tests/sh/test_install.sh
echo "alpine suite passed"
