// Package testutil holds the helpers every test uses so that no test touches the real home,
// temp dir, scheduler or any real repository.
package testutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
)

// TempHome points HOME, XDG_*, TMPDIR and SAPULIDI_* into one temp tree and returns its root.
// The environment is restored when the test ends.
func TempHome(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	dirs := map[string]string{
		"HOME":            root,
		"XDG_CONFIG_HOME": filepath.Join(root, ".config"),
		"XDG_STATE_HOME":  filepath.Join(root, ".local", "state"),
		"XDG_DATA_HOME":   filepath.Join(root, ".local", "share"),
		"XDG_CACHE_HOME":  filepath.Join(root, ".cache"),
		"TMPDIR":          filepath.Join(root, "tmp"),
		"SAPULIDI_CONFIG": filepath.Join(root, ".config", "sapulidi"),
		"SAPULIDI_STATE":  filepath.Join(root, ".local", "state", "sapulidi"),
		"SAPULIDI_DATA":   filepath.Join(root, ".local", "share", "sapulidi"),
	}
	for k, v := range dirs {
		if k != "HOME" {
			if err := os.MkdirAll(v, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv(k, v)
	}
	return root
}

func runGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// MakeRepo creates a git repository at dir with one commit, a local identity, a bare `origin`
// next to it in a temp dir, and HEAD pushed. It returns dir.
func MakeRepo(t testing.TB, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, "", "init", "--bare", "-b", "main", bare)
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.name", "Sapulidi Test")
	runGit(t, dir, "config", "user.email", "test@example.invalid")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README")
	runGit(t, dir, "commit", "-m", "initial")
	runGit(t, dir, "remote", "add", "origin", bare)
	runGit(t, dir, "push", "-u", "origin", "main")
	return dir
}

// Touch creates path when missing (with parents) and sets its mtime to age ago.
// ctime cannot be set; tests that need an old ctime set SAPULIDI_TEST_NO_CTIME=1.
func Touch(t testing.TB, path string, age time.Duration) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mt := time.Now().Add(-age)
	if err := os.Chtimes(path, mt, mt); err != nil {
		t.Fatal(err)
	}
}

// FakePlatform answers every core.Platform method from its fields; zero value is safe.
type FakePlatform struct {
	OSName        string // default runtime.GOOS
	WSL           bool
	Temp          string
	TempErr       error
	Scratch       []string
	Open          map[string]bool // path → open
	OpenErr       error
	Mentioned     map[string]bool
	MentionErr    error
	OtherDevice   map[string]bool // path (either argument) on another device
	SameDevErr    error
	NotOwned      map[string]bool
	OwnedErr      error
	Free          uint64
	FreeErr       error
	NotifyErr     error
	Notifications []string // "title: body"
	TTY           bool
}

func (p *FakePlatform) OS() string {
	if p.OSName != "" {
		return p.OSName
	}
	return runtime.GOOS
}
func (p *FakePlatform) IsWSL() bool                      { return p.WSL }
func (p *FakePlatform) TempDir() (string, error)         { return p.Temp, p.TempErr }
func (p *FakePlatform) ScratchBases() []string           { return p.Scratch }
func (p *FakePlatform) IsOpen(path string) (bool, error) { return p.Open[path], p.OpenErr }
func (p *FakePlatform) ArgsMention(path string) (bool, error) {
	return p.Mentioned[path], p.MentionErr
}
func (p *FakePlatform) SameDevice(a, b string) (bool, error) {
	return !(p.OtherDevice[a] || p.OtherDevice[b]), p.SameDevErr
}
func (p *FakePlatform) OwnedByUser(path string) (bool, error) { return !p.NotOwned[path], p.OwnedErr }
func (p *FakePlatform) FreeBytes(string) (uint64, error)      { return p.Free, p.FreeErr }
func (p *FakePlatform) Notify(title, body string) error {
	p.Notifications = append(p.Notifications, title+": "+body)
	return p.NotifyErr
}
func (p *FakePlatform) HasTTY() bool { return p.TTY }

// Call is one recorded FakeRunner invocation.
type Call struct {
	Dir  string
	Argv []string
}

type script struct {
	prefix []string
	out    string
	err    error
}

// FakeRunner answers scripted argv prefixes, records every call, and runs the real git
// for unscripted `git` commands only. Anything else unscripted is an error.
type FakeRunner struct {
	Calls   []Call
	Paths   map[string]string // LookPath answers; missing names are not found (git excepted)
	scripts []script
}

// On scripts the answer for commands whose argv starts with prefix; the longest prefix wins.
func (r *FakeRunner) On(prefix []string, stdout string, err error) {
	r.scripts = append(r.scripts, script{prefix, stdout, err})
}

func (r *FakeRunner) Run(ctx context.Context, dir string, argv ...string) ([]byte, error) {
	r.Calls = append(r.Calls, Call{Dir: dir, Argv: append([]string(nil), argv...)})
	var best *script
	for i := range r.scripts {
		s := &r.scripts[i]
		if len(s.prefix) <= len(argv) && equal(s.prefix, argv[:len(s.prefix)]) &&
			(best == nil || len(s.prefix) >= len(best.prefix)) {
			best = s
		}
	}
	if best != nil {
		return []byte(best.out), best.err
	}
	if len(argv) > 0 && argv[0] == "git" {
		return execx.Real{}.Run(ctx, dir, argv...)
	}
	return nil, fmt.Errorf("fakerunner: unscripted command %v", argv)
}

func (r *FakeRunner) LookPath(name string) (string, error) {
	if p, ok := r.Paths[name]; ok {
		return p, nil
	}
	if name == "git" {
		return exec.LookPath("git")
	}
	return "", fmt.Errorf("fakerunner: %s not found", name)
}

func equal(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FakeSettings is a core.Settings built from fields; zero value means nothing is consented.
type FakeSettings struct {
	Categories    map[string]bool // explicit per-category answer; missing: true for auto categories
	DisabledRules map[string]bool
	Ages          map[string]time.Duration
	ConsentAll    bool
	ConsentedKeys map[string]bool
	LowDisk       uint64
	Growth        int64
	Notify        bool
}

func (s *FakeSettings) CategoryEnabled(c string) bool {
	if v, ok := s.Categories[c]; ok {
		return v
	}
	b, ok := core.CategoryBucket(c)
	return ok && b == core.Auto
}
func (s *FakeSettings) RuleEnabled(k string) bool { return !s.DisabledRules[k] }
func (s *FakeSettings) Age(c string, def time.Duration) time.Duration {
	if v, ok := s.Ages[c]; ok {
		return v
	}
	return def
}
func (s *FakeSettings) Consented(k string) bool   { return s.ConsentAll || s.ConsentedKeys[k] }
func (s *FakeSettings) LowDiskBytes() uint64      { return s.LowDisk }
func (s *FakeSettings) GrowthBytesPerWeek() int64 { return s.Growth }
func (s *FakeSettings) NotifyEnabled() bool       { return s.Notify }

// LogEvent is one MemLogger entry.
type LogEvent struct {
	Kind   string
	Fields map[string]any
}

// MemLogger keeps events in memory.
type MemLogger struct{ Events []LogEvent }

func (l *MemLogger) Event(kind string, fields map[string]any) {
	l.Events = append(l.Events, LogEvent{kind, fields})
}

// MemFacts implements core.FactSink in memory.
type MemFacts struct{ Facts map[string]string }

func (m *MemFacts) Fact(key, text string) {
	if m.Facts == nil {
		m.Facts = map[string]string{}
	}
	m.Facts[key] = text
}

// Keys returns the fact keys sorted, for stable assertions.
func (m *MemFacts) Keys() []string {
	var ks []string
	for k := range m.Facts {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
