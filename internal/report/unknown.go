package report

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/als0x/sapulidi/internal/categories/models"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/report/sizing"
)

const scanDepth = 3

// unknownMin is the size over which an unknown directory is reported (a variable for tests).
var unknownMin int64 = 1 << 30

// ScanOptions bounds one unknown-directory scan: the time budget, the size a directory must reach and the most
// directories returned.
type ScanOptions struct {
	Budget   time.Duration
	MinBytes int64
	Max      int
}

// scanBases are the places searched: every root, the user cache folder and ~/.local/share. A base inside
// another one, or inside Downloads, Documents or Desktop, is dropped.
func scanBases(env *core.Env) []string {
	var cand []string
	for _, r := range env.Roots {
		cand = append(cand, r.Path)
	}
	if env.Platform != nil && env.Platform.OS() == "darwin" {
		cand = append(cand, filepath.Join(env.Home, "Library", "Caches"))
	} else {
		cand = append(cand, filepath.Join(env.Home, ".cache"))
	}
	cand = append(cand, filepath.Join(env.Home, ".local", "share"))
	sort.Strings(cand)
	var out []string
	for _, b := range cand {
		if fi, err := os.Lstat(b); err != nil || !fi.IsDir() || inside(b, personalDirs(env)) {
			continue
		}
		if n := len(out); n > 0 && (out[n-1] == b || strings.HasPrefix(b, out[n-1]+string(filepath.Separator))) {
			continue
		}
		out = append(out, b)
	}
	return out
}

func inside(p string, dirs []string) bool {
	for _, d := range dirs {
		if p == d || strings.HasPrefix(p, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func devOf(fi fs.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}

// walkBase adds the size of every entry to its directories up to depth 3. It stops when ctx ends. A directory in
// skip, and a directory on another device than the base, is left out with everything inside it.
func walkBase(ctx context.Context, base string, skip map[string]bool, sizes map[string]int64) {
	var baseDev uint64
	haveDev := false
	if fi, err := os.Lstat(base); err == nil {
		baseDev, haveDev = devOf(fi)
	}
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == base {
			return nil
		}
		if d.IsDir() && skip[p] {
			return fs.SkipDir
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if dev, ok := devOf(fi); ok && haveDev && d.IsDir() && dev != baseDev {
			return fs.SkipDir
		}
		parts := strings.Split(strings.TrimPrefix(p, base+string(filepath.Separator)), string(filepath.Separator))
		n := len(parts)
		if !d.IsDir() {
			n--
		}
		sz := sizing.Allocated(fi)
		for i := 1; i <= n && i <= scanDepth; i++ {
			sizes[filepath.Join(base, filepath.Join(parts[:i]...))] += sz
		}
		return nil
	})
}

// ScanUnknown sizes the directories up to depth 3 under the scan bases, leaving out covered paths, the personal
// folders, the model stores and other filesystems (and what is inside them), and returns those of at least
// MinBytes, largest first, at most Max. A directory that has a reported directory inside it is dropped, so the
// heavy place is named, not every parent. When the budget (or env.Ctx) runs out, or a walk is blocked past the
// deadline, the scan stops, a blocked base is left out, and every returned size is marked as not known.
func ScanUnknown(env *core.Env, covered []string, o ScanOptions) []DirUsage {
	ctx, cancel := context.WithTimeout(sizing.Parent(env.Ctx), o.Budget)
	defer cancel()
	skip := map[string]bool{}
	for _, c := range covered {
		skip[c] = true
	}
	for _, p := range personalDirs(env) {
		skip[p] = true
	}
	for _, m := range models.Stores(env) {
		skip[m.Path] = true
	}
	sizes := map[string]int64{}
	for _, base := range scanBases(env) {
		part := map[string]int64{}
		if !sizing.Within(ctx, func() { walkBase(ctx, base, skip, part) }) {
			break // the walk is blocked; part may still be written to, so it is not read
		}
		for k, v := range part {
			sizes[k] += v
		}
		if ctx.Err() != nil {
			break
		}
	}
	complete := ctx.Err() == nil
	big := []DirUsage{}
	hasChild := map[string]bool{}
	for p, n := range sizes {
		if n >= o.MinBytes {
			big = append(big, DirUsage{Path: p, Bytes: n, SizeKnown: complete})
			for a := filepath.Dir(p); a != "/" && a != "." && a != filepath.Dir(a); a = filepath.Dir(a) {
				hasChild[a] = true
			}
		}
	}
	out := big[:0]
	for _, d := range big {
		if !hasChild[d.Path] {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Path < out[j].Path
	})
	if o.Max > 0 && len(out) > o.Max {
		out = out[:o.Max]
	}
	return out
}
