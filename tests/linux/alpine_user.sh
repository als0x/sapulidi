#!/bin/sh
# Runs as the unprivileged user inside the Alpine container (started by inside_alpine.sh): the pty tests. The install
# harness is bash and this image has no bash, so install.sh is not exercised under BusyBox ash here: it is skipped.
set -eu
export HOME=/home/user GOPATH=/home/user/go GOMODCACHE=/home/user/go/pkg/mod GOCACHE=/home/user/.cache/go-build
export PATH=/go/bin:/usr/local/go/bin:/usr/bin:/bin CGO_ENABLED=0
cd /work
echo "shell: $(readlink -f /bin/sh)"
sh tests/linux/pty_check.sh
echo "skip: install.sh under BusyBox ash (the harness needs bash; this image has none)"
echo "alpine pty leg passed"
