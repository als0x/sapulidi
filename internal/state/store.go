// Package state holds what sapulidi remembers between runs: consent, the run lock, the pause file, the log,
// "notify once" facts, growth samples, host heartbeats and the proposals folder. Everything lives in the state
// directory (spec 4.2); files are written atomically with mode 0600 and directories are 0700.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/state/atomic"
)

// Consent is the record `setup` writes: when, and the rule keys (with revision) the user confirmed.
type Consent = config.Consent

// Store is the state directory of one sapulidi installation.
type Store struct {
	dir string
	now func() time.Time

	afterStaleCheck func() // test hook: runs between the staleness check and the take-over rename
	afterRename     func() // test hook: runs after the stale lock was moved, before it is verified

	mu sync.Mutex // serialises file read-modify-write inside this process
}

// Open creates the state directory (and the hosts and proposals folders inside it) and refuses a symlinked one.
func Open(p config.Paths) (*Store, error) {
	if !filepath.IsAbs(p.State) {
		return nil, fmt.Errorf("state: path %q is not absolute", p.State)
	}
	if err := os.MkdirAll(filepath.Dir(p.State), 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: filepath.Clean(p.State), now: time.Now}
	for _, d := range []string{s.dir, s.path("hosts"), s.path("proposals")} {
		if err := ensureDir(d); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// ensureDir creates dir with mode 0700 when missing and fails when it is a symlink or not a directory.
func ensureDir(dir string) error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		fi, err = os.Lstat(dir)
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state: %s is a symlink; refusing to use it", dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("state: %s is not a directory", dir)
	}
	if fi.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700) // ours to tighten: state holds the log and consent
	}
	return nil
}

func (s *Store) path(name ...string) string {
	return filepath.Join(append([]string{s.dir}, name...)...)
}

// ProposalsDir is where the scheduled agent writes proposals.
func (s *Store) ProposalsDir() string { return s.path("proposals") }

func (s *Store) writeJSON(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return atomic.WriteFile(s.path(name), append(b, '\n'))
}

// readJSON reads name into v; a missing file reports false.
func (s *Store) readJSON(name string, v any) (bool, error) {
	b, err := os.ReadFile(s.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, fmt.Errorf("state: %s: %w", name, err)
	}
	return true, nil
}

type consentFile struct {
	At    time.Time `json:"at"`
	Rules []string  `json:"rules"`
}

// Consent returns the recorded consent, or nil when `setup` has not run.
func (s *Store) Consent() (*Consent, error) {
	var f consentFile
	ok, err := s.readJSON("consent.json", &f)
	if err != nil || !ok {
		return nil, err
	}
	return &Consent{At: f.At, Rules: f.Rules}, nil
}

func (s *Store) SaveConsent(c *Consent) error {
	if c == nil {
		return errors.New("state: nil consent")
	}
	return s.writeJSON("consent.json", consentFile{At: c.At, Rules: c.Rules})
}

// Paused reports whether the pause file exists.
func (s *Store) Paused() bool {
	// Fail closed: only a missing file means "not paused"; any other error (an unreadable state directory) stops
	// the run, because a pause the user set could be hiding behind it.
	_, err := os.Lstat(s.path("pause"))
	return !errors.Is(err, fs.ErrNotExist)
}

func (s *Store) Pause() {
	_ = atomic.WriteFile(s.path("pause"), []byte(s.now().UTC().Format(time.RFC3339)+"\n"))
}

func (s *Store) Resume() { _ = os.Remove(s.path("pause")) }

type runFile struct {
	T       time.Time `json:"t"`
	Summary any       `json:"summary"`
}

// RunRecord stores the summary of a run as the last run and logs it as a "run" event.
func (s *Store) RunRecord(summary any) {
	t := s.now().UTC()
	_ = s.writeJSON("lastrun.json", runFile{T: t, Summary: summary})
	s.log("run", map[string]any{"summary": summary})
}

// LastRun is the time of the last RunRecord.
func (s *Store) LastRun() (time.Time, bool) {
	var f runFile
	ok, err := s.readJSON("lastrun.json", &f)
	if err != nil || !ok {
		return time.Time{}, false
	}
	return f.T, true
}
