package packsload

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/als0x/sapulidi/internal/core"
)

// Error is one validation failure; it names the pack, the rule and the field (spec S13).
type Error struct {
	Pack, Rule, Field, Msg string
}

func (e *Error) Error() string {
	s := "pack " + e.Pack
	if e.Rule != "" {
		s += " rule " + e.Rule
	}
	if e.Field != "" {
		s += " field " + e.Field
	}
	return s + ": " + e.Msg
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

// reserved base names no pattern may be able to match: source-like names and .git.
var reserved = []string{".git", "src", "lib", "app", "docs", "test", "tests"}

const (
	autoMinAge     = time.Hour
	optionalMinAge = 24 * time.Hour
)

func recommendError(r core.Rule) error {
	return &Error{Pack: r.Pack, Rule: r.ID, Field: "recommend",
		Msg: fmt.Sprintf("only allowed on bucket %q, not %q", core.Optional, r.Bucket)}
}

// Validate checks one rule against the schema and the registries of internal/core.
func Validate(r core.Rule) error {
	fail := func(field, format string, a ...any) error {
		return &Error{Pack: r.Pack, Rule: r.ID, Field: field, Msg: fmt.Sprintf(format, a...)}
	}
	if !idRe.MatchString(r.Pack) {
		return fail("pack", "name %q does not match %s", r.Pack, idRe)
	}
	if !idRe.MatchString(r.ID) {
		return fail("id", "%q does not match %s", r.ID, idRe)
	}
	if r.Rev < 1 {
		return fail("rev", "must be at least 1, got %d", r.Rev)
	}
	catBucket, ok := core.CategoryBucket(r.Category)
	if !ok {
		return fail("category", "unknown category %q", r.Category)
	}
	switch r.Bucket {
	case core.Auto, core.Optional, core.Never:
	default:
		return fail("bucket", "unknown bucket %q", r.Bucket)
	}
	if r.Bucket != catBucket && r.Bucket != core.Never {
		return fail("bucket", "%q does not match the bucket %q of category %q", r.Bucket, catBucket, r.Category)
	}
	switch r.Kind {
	case core.KindTemp, core.KindProject, core.KindFile, core.KindCache, core.KindVersions, core.KindPath:
	default:
		return fail("kind", "unknown kind %q", r.Kind)
	}
	if r.Recommend && r.Bucket != core.Optional {
		return recommendError(r)
	}

	needNames := r.Kind == core.KindTemp || r.Kind == core.KindProject || r.Kind == core.KindFile
	needMarkers := r.Kind == core.KindProject || r.Kind == core.KindFile
	if len(r.Names) > 0 && !needNames {
		return fail("names", "not allowed for kind %q", r.Kind)
	}
	if len(r.Markers) > 0 && !needMarkers {
		return fail("markers", "not allowed for kind %q", r.Kind)
	}
	if r.Tool != "" && r.Kind != core.KindCache {
		return fail("tool", "only allowed for kind %q", core.KindCache)
	}
	if r.Action != "" && r.Kind != core.KindCache {
		return fail("action", "only allowed for kind %q", core.KindCache)
	}
	if r.Strategy != "" && r.Kind != core.KindVersions {
		return fail("strategy", "only allowed for kind %q", core.KindVersions)
	}
	if r.Location != "" && r.Kind != core.KindVersions && r.Kind != core.KindPath {
		return fail("location", "not allowed for kind %q", r.Kind)
	}
	if needNames && len(r.Names) == 0 {
		return fail("names", "required for kind %q", r.Kind)
	}
	for _, p := range r.Names {
		if err := checkPattern(p); err != nil {
			return fail("names", "%v", err)
		}
	}
	if needMarkers && len(r.Markers) == 0 {
		return fail("markers", "required for kind %q", r.Kind)
	}
	for _, p := range r.Markers {
		// A marker only has to exist next to a candidate, so the exact name .git is fine there: it marks a
		// repository root. Every other protected name stays refused.
		if err := checkGlob(p, p != ".git"); err != nil {
			return fail("markers", "%v", err)
		}
	}

	min := time.Duration(0)
	switch r.Bucket {
	case core.Auto:
		min = autoMinAge
	case core.Optional:
		min = optionalMinAge
	}
	if r.MinAge < min {
		return fail("min_age", "%s is below the %s minimum of bucket %q", r.MinAge, min, r.Bucket)
	}

	switch r.Kind {
	case core.KindCache:
		if !core.ToolActionKnown(r.Tool, r.Action) {
			return fail("tool", "unknown tool/action %q/%q", r.Tool, r.Action)
		}
	case core.KindVersions:
		if !core.StrategyKnown(r.Strategy) {
			return fail("strategy", "unknown strategy %q", r.Strategy)
		}
		if !core.LocationKnown(r.Location) {
			return fail("location", "unknown location %q", r.Location)
		}
	case core.KindPath:
		if !core.LocationKnown(r.Location) {
			return fail("location", "unknown location %q", r.Location)
		}
	}
	for _, o := range r.OS {
		if o != "darwin" && o != "linux" {
			return fail("os", "%q is not darwin or linux", o)
		}
	}
	return nil
}

// checkPattern applies the pattern rules for names and markers: a short, plain glob on a base name only
// that cannot match .git or a source-like directory in any case (case-insensitive filesystems).
func checkPattern(p string) error { return checkGlob(p, true) }

// checkGlob is checkPattern with the protected-name check optional (markers skip it).
func checkGlob(p string, protect bool) error {
	switch {
	case p == "":
		return fmt.Errorf("empty pattern")
	case len(p) > 64:
		return fmt.Errorf("pattern is longer than 64 bytes")
	case p == ".":
		return fmt.Errorf("pattern %q is the current directory", p)
	case strings.HasPrefix(p, "~"):
		return fmt.Errorf("pattern %q starts with ~", p)
	case strings.Contains(p, "/"):
		return fmt.Errorf("pattern %q contains /", p)
	case strings.Contains(p, ".."):
		return fmt.Errorf("pattern %q contains ..", p)
	}
	for _, c := range p {
		if unicode.IsControl(c) || unicode.IsSpace(c) {
			return fmt.Errorf("pattern %q contains a control or white space character", p)
		}
	}
	if _, err := path.Match(p, ""); err != nil {
		return fmt.Errorf("pattern %q is invalid: %v", p, err)
	}
	literal, wild := 0, 0
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '*', '?':
			wild++
		case '[': // a whole class counts as one wildcard
			wild++
			if j := strings.IndexByte(p[i:], ']'); j > 0 {
				i += j
			}
		case '\\':
			literal++
			i++
		default:
			literal++
		}
	}
	if literal < 3 || wild > 3 {
		return fmt.Errorf("pattern %q needs at least 3 literal characters and at most 3 wildcards", p)
	}
	if !protect {
		return nil
	}
	lower := strings.ToLower(p)
	for _, name := range reserved {
		forms := []string{name, strings.ToUpper(name), capitalise(name), capitalise(strings.TrimPrefix(name, ".")), "." + capitalise(strings.TrimPrefix(name, "."))}
		for _, f := range forms {
			if ok, _ := path.Match(p, f); ok {
				return fmt.Errorf("pattern %q would match protected name %q", p, f)
			}
		}
		if ok, _ := path.Match(lower, name); ok {
			return fmt.Errorf("pattern %q would match protected name %q", p, name)
		}
	}
	return nil
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
