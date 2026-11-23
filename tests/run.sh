#!/usr/bin/env bash
# The one check: gofmt, vet for both OSes, go test, then every tests/sh/test_*.sh.
# Works with bash 3.2 (macOS) and Linux bash. Stops at the first failure.
set -eu
cd "$(dirname "$0")/.."

bad=$(find . \( -path ./.worktrees -o -path ./.chief -o -path ./.git \) -prune -o -name '*.go' -print | xargs gofmt -l)
if [ -n "$bad" ]; then
	echo "gofmt needed for:" >&2
	echo "$bad" >&2
	exit 1
fi

go vet ./...
GOOS=linux go vet ./...
GOOS=darwin go vet ./...
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /dev/null ./cmd/sapulidi
go test -count=1 ./...

for t in tests/sh/test_*.sh; do
	[ -e "$t" ] || continue
	echo "== $t"
	bash "$t"
done
echo "all checks passed"
