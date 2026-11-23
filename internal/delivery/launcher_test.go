package delivery

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestLauncherScript runs the shell test suite for bin/sapulidi with sh-compatible bash.
func TestLauncherScript(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	script, err := filepath.Abs("../../tests/sh/test_launcher.sh")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", script).CombinedOutput()
	if err != nil {
		t.Fatalf("launcher tests failed: %v\n%s", err, out)
	}
}
