#!/usr/bin/env bash
# End-to-end scenario on Linux, as an unprivileged user, in a sandbox home. Old leftovers are removed and young
# ones stay; the cron scheduler is installed and doctor runs. Run by as_user.sh inside the container.
# SAPULIDI_TEST=1 with SAPULIDI_TEST_NO_CTIME=1 lets the planted mtimes decide age (ctime cannot be set back).
set -eu
cd /work

W=/home/user/scenario
rm -rf "$W"
mkdir -p "$W/h" "$W/tmp" "$W/bin"
export HOME="$W/h" TMPDIR="$W/tmp" SAPULIDI_TEST=1 SAPULIDI_TEST_NO_CTIME=1
export SAPULIDI_CONFIG="$W/config" SAPULIDI_STATE="$W/state" SAPULIDI_DATA="$W/data"
BIN="$W/bin/sapulidi"
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o "$BIN" ./cmd/sapulidi

fail=0
check() { # check <description> <command...>
	local desc=$1
	shift
	if "$@"; then
		echo "ok: $desc"
	else
		echo "FAIL: $desc" >&2
		fail=1
	fi
}

# Temp leftovers: one old go-build directory, one young one.
mkdir -p "$W/tmp/go-build-old/obj" "$W/tmp/go-build-young/obj"
echo old >"$W/tmp/go-build-old/obj/f"
echo young >"$W/tmp/go-build-young/obj/f"
find "$W/tmp/go-build-old" -exec touch -h -d "60 days ago" {} +

# A git project with an ignored node_modules: old (untouched 60 days) and young.
for name in old-app young-app; do
	P="$W/h/src/$name"
	mkdir -p "$P/node_modules/pkg"
	echo '{"name":"'"$name"'"}' >"$P/package.json"
	echo "node_modules/" >"$P/.gitignore"
	echo "module.exports = 1" >"$P/node_modules/pkg/index.js"
	git -C "$P" init -q
	git -C "$P" add package.json .gitignore
	git -C "$P" -c user.name=sapulidi-test -c user.email=test@example.invalid commit -q -m init
done
find "$W/h/src/old-app" -exec touch -h -d "60 days ago" {} +

# sapulidi never scans home on its own: the projects are in scope only below a configured root.
mkdir -p "$W/config"
# deps (node_modules and the like) is an optional category: off until the config turns it on.
printf 'roots = ["%s"]\n[category.deps]\nenabled = true\n' "$W/h/src" >"$W/config/config.toml"

check "setup --yes records consent" "$BIN" setup --yes
check "run" "$BIN" run

check "old go-build directory is removed" test ! -e "$W/tmp/go-build-old"
check "young go-build directory stays" test -f "$W/tmp/go-build-young/obj/f"
check "old node_modules is removed" test ! -e "$W/h/src/old-app/node_modules"
check "young node_modules stays" test -f "$W/h/src/young-app/node_modules/pkg/index.js"

check "schedule install" "$BIN" schedule install
check "cron entry is installed" sh -c 'crontab -l | grep -q sapulidi'
check "doctor" "$BIN" doctor

if [ "$fail" -ne 0 ]; then
	echo "scenario FAILED" >&2
	exit 1
fi
echo "scenario passed"
