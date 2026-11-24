#!/bin/bash
# Stand-in for tests/run.sh in the copy of the repository: logs that the suite ran and what checksums.txt held.
lines=0
[ ! -e checksums.txt ] || lines=$(wc -l <checksums.txt | tr -d ' ')
echo "suite checksum_lines=$lines checksums=$(cksum <checksums.txt | tr ' ' :)" >>"$FAKE_LOG"
[ "${FAKE_SUITE_FAIL:-0}" != 1 ] || exit 1
exit 0
