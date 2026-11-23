//go:build !linux

package term

import "testing"

func TestPTYSkippedOffLinux(t *testing.T) {
	t.Skip("pty tests run on Linux in the container suite (LX2); on macOS they are checked by hand in LG3")
}
