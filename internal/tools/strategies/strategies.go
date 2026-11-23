// Package strategies holds the named, pure filters that decide which installed versions of a toolchain or a
// browser may go (spec S13). A strategy only sorts versions into "remove" and "keep"; it never touches the disk.
// The category finds the versions and which ones are active or pinned, and answers every doubt with "unknown",
// for which every strategy keeps everything.
package strategies

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/als0x/sapulidi/internal/core"
)

const (
	KeepActiveAndPrevious = "keep-active-and-previous"
	KeepNewestPerName     = "keep-newest-per-name"
	NotPinnedInRoots      = "not-pinned-in-roots"
)

// Version is one installed version: Name is what pins and the active marker refer to, Path its directory.
type Version struct{ Name, Path string }

// Input is what a location found. Active and Pinned list version names; ActiveKnown is false when the active
// version could not be determined for sure.
type Input struct {
	Versions    []Version
	Active      []string
	ActiveKnown bool
	Pinned      []string
}

// Decision lists what may be removed and why every other version stays (a core.Kept token per name).
type Decision struct {
	Remove []Version
	Keep   map[string]string
}

var impls = map[string]func(Input) Decision{
	KeepActiveAndPrevious: keepActiveAndPrevious,
	KeepNewestPerName:     keepNewestPerName,
	NotPinnedInRoots:      notPinnedInRoots,
}

func init() {
	for id := range impls {
		core.RegisterStrategy(id)
	}
}

// Apply runs the strategy with the given id; ok is false for an unknown id.
func Apply(id string, in Input) (Decision, bool) {
	f, ok := impls[id]
	if !ok {
		return Decision{}, false
	}
	return f(in), true
}

func keepAll(in Input, why string) Decision {
	d := Decision{Keep: map[string]string{}}
	for _, v := range in.Versions {
		d.Keep[v.Name] = why
	}
	return d
}

// finish removes every version that has no keep entry.
func finish(in Input, keep map[string]string) Decision {
	d := Decision{Keep: keep}
	for _, v := range in.Versions {
		if _, ok := keep[v.Name]; !ok {
			d.Remove = append(d.Remove, v)
		}
	}
	return d
}

// markProtected keeps active and pinned versions; every strategy does this first.
func markProtected(in Input, keep map[string]string) {
	for _, v := range in.Versions {
		if slices.Contains(in.Active, v.Name) || slices.Contains(in.Pinned, v.Name) {
			keep[v.Name] = "active"
		}
	}
}

// keepActiveAndPrevious keeps the active version, pinned ones, and the newest version besides those.
func keepActiveAndPrevious(in Input) Decision {
	if !in.ActiveKnown {
		return keepAll(in, "error")
	}
	keep := map[string]string{}
	markProtected(in, keep)
	var newest *Version
	for i := range in.Versions {
		v := &in.Versions[i]
		if _, ok := keep[v.Name]; ok {
			continue
		}
		if newest == nil || Compare(v.Name, newest.Name) > 0 {
			newest = v
		}
	}
	if newest != nil {
		keep[newest.Name] = "active"
	}
	return finish(in, keep)
}

// notPinnedInRoots keeps the active version and every pinned one, and removes the rest.
func notPinnedInRoots(in Input) Decision {
	if !in.ActiveKnown {
		return keepAll(in, "error")
	}
	keep := map[string]string{}
	markProtected(in, keep)
	return finish(in, keep)
}

var buildRe = regexp.MustCompile(`^(.+)-(\d+)$`)

// keepNewestPerName keeps the highest build number of every browser name (chromium-1148 over chromium-1140).
// A name without a build number is kept. Active and pinned names are kept as well.
func keepNewestPerName(in Input) Decision {
	keep := map[string]string{}
	markProtected(in, keep)
	best := map[string]*Version{}
	for i := range in.Versions {
		v := &in.Versions[i]
		m := buildRe.FindStringSubmatch(v.Name)
		if m == nil {
			keep[v.Name] = "error"
			continue
		}
		if b, ok := best[m[1]]; !ok || Compare(v.Name, b.Name) > 0 {
			best[m[1]] = v
		}
	}
	for _, v := range best {
		if _, ok := keep[v.Name]; !ok {
			keep[v.Name] = "active"
		}
	}
	return finish(in, keep)
}

// Compare orders two version names: runs of digits compare as numbers, everything else as text. It returns
// a negative number, zero or a positive number.
func Compare(a, b string) int {
	sa, sb := segments(a), segments(b)
	for i := 0; i < len(sa) && i < len(sb); i++ {
		x, y := sa[i], sb[i]
		nx, ex := strconv.Atoi(x)
		ny, ey := strconv.Atoi(y)
		switch {
		case ex == nil && ey == nil:
			if nx != ny {
				return nx - ny
			}
		case ex == nil: // a number sorts above text
			return 1
		case ey == nil:
			return -1
		default:
			if px, py := isPre(x), isPre(y); px != py {
				if px {
					return -1
				}
				return 1
			}
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(sa) > len(sb) && isPre(sa[len(sb)]):
		return -1 // v20.0.0-rc.1 is below v20.0.0
	case len(sb) > len(sa) && isPre(sb[len(sa)]):
		return 1
	}
	return len(sa) - len(sb)
}

// isPre reports a text segment that starts a pre-release: it begins with "-" or a letter, not with ".".
func isPre(s string) bool {
	c := s[0]
	return c == '-' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

var segRe = regexp.MustCompile(`\d+|[^\d]+`)

func segments(s string) []string { return segRe.FindAllString(s, -1) }
