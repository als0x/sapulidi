// Package exclude is the user's persistent exclusion list: paths (or globs) sapulidi must never remove.
// The file is <config dir>/exclude.toml. This package only stores and matches; guard.Excluded applies it.
package exclude

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/state/atomic"
)

// FileName is the exclusion file inside the config directory.
const FileName = "exclude.toml"

// Entry is one [[exclude]] table.
type Entry struct {
	Path  string `toml:"path"`
	Added string `toml:"added"` // YYYY-MM-DD
	Note  string `toml:"note,omitempty"`
}

type fileDoc struct {
	Exclude []Entry `toml:"exclude"`
}

// CorruptError means exclude.toml cannot be read or parsed; nothing may run until it is fixed.
type CorruptError struct {
	File string
	Err  error
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("exclude file %s cannot be read (%v): nothing will run until it is fixed", e.File, e.Err)
}

func (e *CorruptError) Unwrap() error { return e.Err }

// List is the loaded exclusion list.
type List struct {
	file    string
	entries []Entry
}

// Load reads <config dir>/exclude.toml. A missing file is an empty list; anything else wrong is a *CorruptError.
func Load(p config.Paths) (*List, error) {
	l := &List{file: filepath.Join(filepath.Dir(p.Config), FileName)}
	data, err := os.ReadFile(l.file)
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, &CorruptError{File: l.file, Err: err}
	}
	var doc fileDoc
	if _, err := toml.Decode(string(data), &doc); err != nil {
		return nil, &CorruptError{File: l.file, Err: err}
	}
	for _, e := range doc.Exclude {
		if !filepath.IsAbs(e.Path) {
			return nil, &CorruptError{File: l.file, Err: fmt.Errorf("path %q is not absolute", e.Path)}
		}
	}
	l.entries = doc.Exclude
	return l, nil
}

// Entries returns a copy of the stored entries.
func (l *List) Entries() []Entry { return append([]Entry(nil), l.entries...) }

// Save writes the list atomically (mode 0600, directory 0700).
func (l *List) Save() error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(fileDoc{Exclude: l.entries}); err != nil {
		return err
	}
	return atomic.WriteFile(l.file, buf.Bytes())
}

// Add stores path (expanded, made absolute, symlinks resolved). When an existing entry already covers it,
// nothing is stored and by names that entry.
func (l *List) Add(p, note string, now time.Time) (added bool, by string, err error) {
	stored, err := normalise(p)
	if err != nil {
		return false, "", err
	}
	for _, e := range l.entries {
		if anyForm(e.Path, []string{stored}, covers) {
			return false, e.Path, nil
		}
	}
	l.entries = append(l.entries, Entry{Path: stored, Added: now.Format("2006-01-02"), Note: note})
	return true, "", nil
}

// Remove deletes the entry stored for p; an unknown path is an error.
func (l *List) Remove(p string) error {
	want := []string{filepath.Clean(p)}
	if n, err := normalise(p); err == nil {
		want = append(want, n)
	}
	for i, e := range l.entries {
		if anyForm(e.Path, want, sameSegs) {
			l.entries = append(l.entries[:i:i], l.entries[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("%s is not excluded", p)
}

// forms are the spellings of a path that count: its lexical form (cleaned) and its real form (symlinks
// resolved). An entry hand-written as /tmp/x protects /private/tmp/x and the other way round.
func forms(p string) []string {
	c := filepath.Clean(p)
	if r := realPath(c); r != c {
		return []string{c, r}
	}
	return []string{c}
}

// anyForm reports whether rel holds between any form of entry and any of the given candidate forms.
func anyForm(entry string, cands []string, rel func(entry, cand string) bool) bool {
	for _, e := range forms(entry) {
		for _, c := range cands {
			if rel(e, c) {
				return true
			}
		}
	}
	return false
}

func sameSegs(a, b string) bool { return covers(a, b) && covers(b, a) }

// Match reports whether the candidate path must be kept: it is an excluded path, below one, or contains one.
// matched is the entry's path as stored. Both the lexical form and the real form of the candidate are
// checked, so a symlink inside an excluded folder is kept even when it points elsewhere.
// Matching ignores case: it can only keep more, never less. A non-absolute candidate never matches
// (guard.Excluded refuses those itself).
func (l *List) Match(candidate string) (matched string, ok bool) {
	if l == nil || !filepath.IsAbs(candidate) {
		return "", false
	}
	cs := forms(candidate)
	for _, e := range l.entries {
		if anyForm(e.Path, cs, covers) || anyForm(e.Path, cs, contains) {
			return e.Path, true
		}
	}
	return "", false
}

// Within is Match without the "contains" direction: the path is an excluded path or below one.
func (l *List) Within(candidate string) (matched string, ok bool) {
	if l == nil || !filepath.IsAbs(candidate) {
		return "", false
	}
	cs := forms(candidate)
	for _, e := range l.entries {
		if anyForm(e.Path, cs, covers) {
			return e.Path, true
		}
	}
	return "", false
}

// Update takes an exclusive lock on <config dir>/exclude.lock, loads the list afresh, runs fn (which
// changes the list and calls Save) and unlocks, so two writers never lose each other's entries.
func Update(p config.Paths, fn func(l *List) error) error {
	dir := filepath.Dir(p.Config)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "exclude.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	l, err := Load(p)
	if err != nil {
		return err
	}
	return fn(l)
}

// realPath resolves symlinks in the existing part of p and appends the rest unchanged.
func realPath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// normalise expands ~, makes the path absolute and resolves symlinks in the part before any glob segment.
func normalise(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("cannot expand ~: no home directory")
		}
		p = filepath.Join(home, p[1:])
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	segs := split(abs)
	for i, s := range segs {
		if strings.Contains(s, "*") {
			head := realPath("/" + strings.Join(segs[:i], "/"))
			return filepath.Join(head, filepath.Join(segs[i:]...)), nil
		}
	}
	return realPath(abs), nil
}

func split(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// segMatch compares the leading segments of entry and cand; n is how many were compared.
// A segment of the entry with * is a glob (same rules as the packs: path.Match); others compare exactly.
// Comparison ignores case, so a case-insensitive filesystem cannot slip past an exclusion.
func segMatch(entry, cand string) (n int, ok bool) {
	es, cs := split(strings.ToLower(entry)), split(strings.ToLower(cand))
	n = len(es)
	if len(cs) < n {
		n = len(cs)
	}
	for i := 0; i < n; i++ {
		if strings.Contains(es[i], "*") {
			if m, err := path.Match(es[i], cs[i]); err != nil || !m {
				return n, false
			}
		} else if es[i] != cs[i] {
			return n, false
		}
	}
	return n, true
}

// covers: cand is the entry or below it.
func covers(entry, cand string) bool {
	n, ok := segMatch(entry, cand)
	return ok && n == len(split(entry))
}

// contains: the entry is below cand (cand is an ancestor of something excluded).
func contains(entry, cand string) bool {
	n, ok := segMatch(entry, cand)
	return ok && n == len(split(cand))
}
