// Package packsload loads rule packs (spec S13): built-in packs from an embedded file system and user packs
// from a directory. A pack with any invalid rule is ignored as a whole and its errors are returned.
package packsload

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/als0x/sapulidi/internal/core"
)

type rawPack struct {
	Pack  string    `toml:"pack"`
	Rules []rawRule `toml:"rule"`
}

type rawRule struct {
	ID       string   `toml:"id"`
	Rev      int      `toml:"rev"`
	Category string   `toml:"category"`
	Bucket   string   `toml:"bucket"`
	Kind     string   `toml:"kind"`
	Names    []string `toml:"names"`
	Markers  []string `toml:"markers"`
	MinAge   *string  `toml:"min_age"`
	Tool     string   `toml:"tool"`
	Action   string   `toml:"action"`
	Strategy string   `toml:"strategy"`
	Location string   `toml:"location"`
	OS       []string `toml:"os"`
	// Recommend is a pointer because the key itself, whatever its value, is refused outside optional rules.
	Recommend *bool `toml:"recommend"`
}

// Load returns the rules of every valid pack in builtin (the *.toml files at its root) and in userDir
// (empty means none), filtered for the running OS. Errors are one per problem; their packs are skipped.
func Load(builtin fs.FS, userDir string) ([]core.Rule, []error) {
	return load(builtin, userDir, runtime.GOOS)
}

// LoadFor is Load for a named OS ("darwin" or "linux") instead of the running one. Tests use it to check a
// pack for an OS other than the one they run on. A pack whose rules are all for another OS loads as empty.
func LoadFor(builtin fs.FS, userDir, goos string) ([]core.Rule, []error) {
	return load(builtin, userDir, goos)
}

func load(builtin fs.FS, userDir, goos string) ([]core.Rule, []error) {
	var rules []core.Rule
	var errs []error
	builtinNames := map[string]bool{}

	if builtin != nil {
		names, err := fs.Glob(builtin, "*.toml")
		if err != nil {
			errs = append(errs, err)
		}
		slices.Sort(names)
		for _, file := range names {
			name := strings.TrimSuffix(file, ".toml")
			builtinNames[name] = true
			data, err := fs.ReadFile(builtin, file)
			if err != nil {
				errs = append(errs, fmt.Errorf("pack %s: %v", name, err))
				continue
			}
			rs, perrs := parsePack(name, data, true)
			errs = append(errs, perrs...)
			rules = append(rules, rs...)
		}
	}

	if userDir != "" {
		var entries []os.DirEntry
		if err := checkUserDir(userDir); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("user packs: %v", err))
			}
		} else {
			var rerr error
			if entries, rerr = os.ReadDir(userDir); rerr != nil {
				errs = append(errs, fmt.Errorf("user packs: %v", rerr))
			}
		}
		for _, e := range entries { // sorted by name
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".toml")
			if builtinNames[name] {
				errs = append(errs, &Error{Pack: name, Msg: "a user pack cannot reuse the name of a built-in pack"})
				continue
			}
			data, err := readUserFile(filepath.Join(userDir, e.Name()))
			if err != nil {
				errs = append(errs, &Error{Pack: name, Msg: err.Error()})
				continue
			}
			rs, perrs := parsePack(name, data, false)
			errs = append(errs, perrs...)
			for i := range rs {
				rs[i].Pack = "user-" + name
			}
			rules = append(rules, rs...)
		}
	}

	out := rules[:0]
	for _, r := range rules {
		if len(r.OS) == 0 || slices.Contains(r.OS, goos) {
			out = append(out, r)
		}
	}
	return out, errs
}

const maxPackSize = 1 << 20

// checkUserDir refuses a user packs directory that is a symlink, not a directory, owned by someone else
// or writable by group or others.
func checkUserDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return errors.New("directory is a symlink")
	case !info.IsDir():
		return errors.New("not a directory")
	case info.Mode().Perm()&0o022 != 0:
		return errors.New("directory is writable by group or others")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return errors.New("directory is not owned by the current user")
	}
	return nil
}

// readUserFile reads a regular file of at most 1 MiB owned by the user from one descriptor: a symlink is
// refused and a FIFO cannot block the open.
func readUserFile(p string) ([]byte, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("not read (symlinks are ignored): %v", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return nil, errors.New("not owned by the current user")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPackSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPackSize {
		return nil, errors.New("larger than 1 MiB")
	}
	return data, nil
}

// parsePack decodes and validates one pack. Any error drops the whole pack. Only a built-in pack may use the
// recommend key.
func parsePack(name string, data []byte, builtin bool) ([]core.Rule, []error) {
	var raw rawPack
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, []error{&Error{Pack: name, Msg: err.Error()}}
	}
	var errs []error
	for _, k := range md.Undecoded() {
		errs = append(errs, &Error{Pack: name, Field: k.String(), Msg: "unknown key"})
	}
	if raw.Pack != name {
		errs = append(errs, &Error{Pack: name, Field: "pack", Msg: fmt.Sprintf("is %q, must equal the file name %q", raw.Pack, name)})
	}

	seen := map[string]bool{}
	var rules []core.Rule
	for _, rr := range raw.Rules {
		if seen[rr.ID] {
			errs = append(errs, &Error{Pack: name, Rule: rr.ID, Field: "id", Msg: "duplicate id in pack"})
			continue
		}
		seen[rr.ID] = true
		if rr.Recommend != nil && !builtin {
			errs = append(errs, &Error{Pack: name, Rule: rr.ID, Field: "recommend", Msg: "only built-in packs can recommend"})
			continue
		}
		r := core.Rule{
			Pack: name, ID: rr.ID, Rev: rr.Rev, Category: rr.Category, Bucket: core.Bucket(rr.Bucket),
			Kind: core.RuleKind(rr.Kind), Names: rr.Names, Markers: rr.Markers, Tool: rr.Tool,
			Action: rr.Action, Strategy: rr.Strategy, Location: rr.Location, OS: rr.OS,
			Recommend: rr.Recommend != nil && *rr.Recommend,
		}
		switch {
		case rr.MinAge != nil:
			d, err := parseAge(*rr.MinAge)
			if err != nil {
				errs = append(errs, &Error{Pack: name, Rule: rr.ID, Field: "min_age", Msg: err.Error()})
				continue
			}
			r.MinAge = d
		case r.Bucket == core.Auto:
			r.MinAge = autoMinAge // absent min_age means the bucket minimum
		case r.Bucket == core.Optional:
			r.MinAge = optionalMinAge
		}
		if err := Validate(r); err != nil {
			errs = append(errs, err)
			continue
		}
		if rr.Recommend != nil && r.Bucket != core.Optional {
			errs = append(errs, recommendError(r))
			continue
		}
		rules = append(rules, r)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return rules, nil
}

// parseAge parses a Go duration, plus whole days written as "30d". Ages above 10 years are errors.
func parseAge(s string) (time.Duration, error) {
	const maxAge = 10 * 365 * 24 * time.Hour
	bad := fmt.Errorf("invalid age %q (at most 10 years)", s)
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.ParseInt(n, 10, 64)
		if err != nil || days < 0 || days > math.MaxInt64/int64(24*time.Hour) || time.Duration(days)*24*time.Hour > maxAge {
			return 0, bad
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 || d > maxAge {
		return 0, bad
	}
	return d, nil
}
