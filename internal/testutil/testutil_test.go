package testutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var isolated = []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME",
	"TMPDIR", "SAPULIDI_CONFIG", "SAPULIDI_STATE", "SAPULIDI_DATA"}

func TestTempHomeIsolates(t *testing.T) {
	before := map[string]string{}
	for _, k := range isolated {
		before[k] = os.Getenv(k)
	}
	var home string
	t.Run("inner", func(t *testing.T) {
		home = TempHome(t)
		for _, k := range isolated {
			v := os.Getenv(k)
			if v != home && !strings.HasPrefix(v, home+string(filepath.Separator)) {
				t.Errorf("%s=%q is outside %q", k, v, home)
			}
		}
	})
	for _, k := range isolated {
		if os.Getenv(k) != before[k] {
			t.Errorf("%s not restored", k)
		}
	}
	if home == "" {
		t.Fatal("no home")
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestMakeRepoHasCommitAndOrigin(t *testing.T) {
	TempHome(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull) // no global identity
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := filepath.Join(t.TempDir(), "repo")
	got := MakeRepo(t, dir)
	if got != dir {
		t.Fatalf("returned %q", got)
	}
	if git(t, dir, "rev-list", "--count", "HEAD") != "1" {
		t.Fatal("want one commit")
	}
	origin := git(t, dir, "remote", "get-url", "origin")
	if origin == "" {
		t.Fatal("no origin")
	}
	if git(t, dir, "rev-parse", "HEAD") != git(t, dir, "rev-parse", "origin/main") {
		t.Fatal("HEAD not pushed to origin/main")
	}
	if git(t, dir, "config", "--local", "user.email") == "" || git(t, dir, "config", "--local", "user.name") == "" {
		t.Fatal("identity not pinned locally")
	}
}

func TestTouchSetsAge(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	Touch(t, p, 48*time.Hour)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(fi.ModTime()); d < 47*time.Hour || d > 49*time.Hour {
		t.Fatalf("age %v", d)
	}
}

func TestFakeRunnerRecordsCalls(t *testing.T) {
	r := &FakeRunner{}
	r.On([]string{"go", "env"}, "x\n", nil)
	out, err := r.Run(context.Background(), "/d", "go", "env", "GOCACHE")
	if err != nil || string(out) != "x\n" {
		t.Fatalf("out %q err %v", out, err)
	}
	if _, err := r.Run(context.Background(), "", "npm", "x"); err == nil {
		t.Fatal("unscripted non-git command must fail")
	}
	if len(r.Calls) != 2 || r.Calls[0].Dir != "/d" || r.Calls[0].Argv[2] != "GOCACHE" {
		t.Fatalf("calls %+v", r.Calls)
	}
	if _, err := r.LookPath("nothing-here-zz"); err == nil {
		t.Fatal("LookPath of unknown should fail")
	}
}

func TestFakeRunnerFallsBackToGit(t *testing.T) {
	r := &FakeRunner{}
	out, err := r.Run(context.Background(), "", "git", "--version")
	if err != nil || !strings.HasPrefix(string(out), "git version") {
		t.Fatalf("out %q err %v", out, err)
	}
}

func TestFakesDefaults(t *testing.T) {
	p := &FakePlatform{}
	if open, err := p.IsOpen("/x"); open || err != nil {
		t.Fatal("IsOpen default must be false")
	}
	if p.OS() == "" {
		t.Fatal("OS default empty")
	}
	l := &MemLogger{}
	l.Event("k", map[string]any{"a": 1})
	if len(l.Events) != 1 || l.Events[0].Kind != "k" {
		t.Fatal("logger")
	}
	f := &MemFacts{}
	f.Fact("k", "t")
	if f.Facts["k"] != "t" {
		t.Fatal("facts")
	}
	s := &FakeSettings{}
	if s.Consented("a/b@1") || s.NotifyEnabled() {
		t.Fatal("defaults must be off")
	}
}
