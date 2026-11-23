//go:build !linux

package main

import "testing"

func TestTUIQuitsAndLeavesCookedMode(t *testing.T) {
	t.Skip("the pseudo-terminal end-to-end test runs on Linux in the container suite; on macOS it is checked by hand")
}
