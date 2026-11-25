#!/bin/sh
# Runs inside the Alpine golang image as root (BusyBox ash): installs bash and git, creates the unprivileged user,
# copies the repository in, then runs the pty tests and install.sh as that user.
set -eu
apk add --no-cache -q bash git >/dev/null
id -u user >/dev/null 2>&1 || adduser -D -s /bin/sh user
mkdir -p /work
tar -C /src --exclude=./.worktrees --exclude=./.chief --exclude=./.git --exclude=./.codegraph --exclude=./.superpowers --exclude=./GEMINI.md -cf - . | tar -C /work -xf -
chown -R user:user /work
su -s /bin/sh user -c "sh /work/tests/linux/alpine_user.sh"
