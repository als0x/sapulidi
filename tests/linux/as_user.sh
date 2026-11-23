#!/usr/bin/env bash
# Runs as the unprivileged user inside the container (started by inside.sh): the repository suite, then the
# end-to-end scenario.
set -eu
export HOME=/home/user
export GOPATH=$HOME/go GOMODCACHE=$HOME/go/pkg/mod GOCACHE=$HOME/.cache/go-build
cd /work
bash tests/run.sh
bash tests/linux/scenario.sh
