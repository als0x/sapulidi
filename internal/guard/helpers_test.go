package guard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/core"
)

// isolateGit keeps the user's git configuration out of every test.
func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func wantWhy(t *testing.T, err error, why string) {
	t.Helper()
	if why == "" {
		if err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		return
	}
	ke, ok := err.(*core.KeepError)
	if !ok {
		t.Fatalf("want KeepError %q, got %v", why, err)
	}
	if ke.Why != why {
		t.Fatalf("want %q, got %q (%s)", why, ke.Why, ke.Detail)
	}
}
