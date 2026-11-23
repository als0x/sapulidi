#!/usr/bin/env bash
# The Linux container suite, in two images, as an unprivileged user, with the repository mounted read-only and copied
# inside:
#   golang:1.26 (Debian, /bin/sh is dash): tests/run.sh, the end-to-end scenario, the TUI scenario in a pty and
#     install.sh under dash.
#   golang:1.26-alpine (BusyBox, /bin/sh is ash): the pty tests and install.sh under ash.
# An image is removed afterwards unless KEEP_IMAGE=1 or it was already present before the run, so an image that was
# there before is never touched. Needs podman. Exits non-zero on any failure.
set -eu
root=$(cd "$(dirname "$0")/../.." && pwd)
rc=0

run_image() { # run_image <image> <shell> <script>
	local image=docker.io/library/$1 shell=$2 script=$3 existed=0 r=0
	podman image exists "$image" && existed=1
	podman run --rm -v "$root":/src:ro "$image" "$shell" "/src/tests/linux/$script" || r=$?
	echo "image $image size $(podman image inspect --format '{{.Size}}' "$image") bytes (existed before: $existed)" >&2
	if [ "$existed" = 0 ] && [ "${KEEP_IMAGE:-0}" != 1 ]; then
		podman rmi "$image" >/dev/null || echo "warning: could not remove $image" >&2
	fi
	return "$r"
}

run_image golang:1.26 bash inside.sh || rc=$?
run_image golang:1.26-alpine sh inside_alpine.sh || rc=$?

if [ "$rc" -ne 0 ]; then
	echo "linux suite FAILED (exit $rc)" >&2
	exit 1
fi
echo "linux suite passed"
