#!/usr/bin/env bash
# The Linux container suite: runs `bash tests/run.sh` and an end-to-end scenario inside golang:1.26 as an
# unprivileged user. The repository is mounted read-only and copied inside; the worktree is never written.
# The image is removed afterwards unless KEEP_IMAGE=1. Needs podman. Exits non-zero on any failure.
set -eu
root=$(cd "$(dirname "$0")/../.." && pwd)
image=docker.io/library/golang:1.26

rc=0
podman run --rm -v "$root":/src:ro "$image" bash /src/tests/linux/inside.sh || rc=$?

if [ "${KEEP_IMAGE:-0}" != 1 ]; then
	podman rmi "$image" >/dev/null || echo "warning: could not remove $image" >&2
fi

if [ "$rc" -ne 0 ]; then
	echo "linux suite FAILED (exit $rc)" >&2
	exit 1
fi
echo "linux suite passed"
