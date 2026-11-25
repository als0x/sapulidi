#!/usr/bin/env bash
# Runs inside the container as root: installs cron and git, creates the unprivileged user, copies the
# repository in (read-write, outside the read-only mount), then runs the suite and the scenario as that user.
set -eu
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq cron git >/dev/null
id -u user >/dev/null 2>&1 || useradd -m -s /bin/bash user
mkdir -p /work
tar -C /src --exclude=./.worktrees --exclude=./.chief --exclude=./.git --exclude=./.codegraph --exclude=./.superpowers --exclude=./GEMINI.md -cf - . | tar -C /work -xf -
chown -R user:user /work
runuser -u user -- env PATH="$PATH" bash /work/tests/linux/as_user.sh
