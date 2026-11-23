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
		if covers(e.Path, stored) {
			return false, e.Path, nil
		}
	}
	l.entries = append(l.entries, Entry{Path: stored, Added: now.Format("2006-01-02"), Note: note})
	return true, "", nil
}

// Remove deletes the entry stored for p; an unknown path is an error.
func (l *List) Remove(p string) error {
	want := map[string]bool{p: true, filepath.Clean(p): true}
	if n, err := normalise(p); err == nil {
		want[n] = true
	}
	for i, e := range l.entries {
		if want[e.Path] {
			l.entries = append(l.entries[:i:i], l.entries[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("%s is not excluded", p)
}

// Match reports whether the candidate path must be kept: it is an excluded path, below one, or contains one.
// matched is the entry's stored path. The candidate is compared by its real path.
func (l *List) Match(candidate string) (matched string, ok bool) {
	if l == nil || !filepath.IsAbs(candidate) {
		return "", false
	}
	c := realPath(candidate)
	for _, e := range l.entries {
		if covers(e.Path, c) || contains(e.Path, c) {
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
	c := realPath(candidate)
	for _, e := range l.entries {
		if covers(e.Path, c) {
			return e.Path, true
		}
	}
	return "", false
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
