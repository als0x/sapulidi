package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/report"
	"github.com/als0x/sapulidi/internal/tui/term"
)

// scanBudget is how long the startup scan may take; the engine default (two minutes) could cut the list short
// without a word.
const scanBudget = 10 * time.Minute

// Facts are the numbers of the summary panel that only the caller can read.
type Facts struct {
	Agents   int           // agent tools the plugin is installed into; 0 when unavailable
	Interval time.Duration // schedule interval; 0 = no job installed
	NextIn   string        // time until the next run as the scheduler reports it, e.g. "12m34s"; "" = unknown
	Paused   bool
}

// EngineDeps is the Deps of the real program: it scans and deletes through the engine and nothing else.
type EngineDeps struct {
	// Build makes a fresh Env; it fails when exclude.toml is corrupt, which stops both scan and delete.
	Build func() (*core.Env, error)
	Cats  []core.Category
	// Run is engine.Run with the caller's store.
	Run       func(env *core.Env, opt engine.Options) (engine.Summary, error)
	Facts     func() Facts
	Exclude   func(path string) error
	Unexclude func(path string) error
	// Open opens the terminal; nil means term.Open.
	Open func() (Terminal, error)

	now     time.Time
	freeNow uint64
	roots   []string
	bases   []string

	planEnv core.Env // the environment of the last scan and its plan: what the uncovered search needs
	plan    engine.Summary
}

// Uncovered implements UncoveredDeps: the search `report` runs for folders no rule covers, over the plan of the last
// scan. It ends with ctx (esc, quit) and says partial then, or when its own 30 s budget ran out.
func (e *EngineDeps) Uncovered(ctx context.Context) ([]Uncovered, bool) {
	env := e.planEnv
	env.Ctx = ctx
	dirs, partial := report.ScanUncovered(&env, e.plan)
	out := make([]Uncovered, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, Uncovered{Path: d.Path, Bytes: d.Bytes})
	}
	return out, partial
}

// withResolved adds the symlink-resolved spelling of each base (/tmp and /private/tmp on macOS) when it differs;
// a base that cannot be resolved keeps only its own spelling.
func withResolved(bases []string) []string {
	out := slices.Clone(bases)
	for _, b := range bases {
		if r, err := filepath.EvalSymlinks(b); err == nil && r != b && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// cleanBases drops empty and relative entries and duplicates, and trims trailing slashes.
func cleanBases(in []string) []string {
	var out []string
	for _, b := range in {
		b = strings.TrimRight(b, "/")
		if b == "" || !filepath.IsAbs(b) || slices.Contains(out, b) {
			continue
		}
		out = append(out, b)
	}
	return out
}

type realTerm struct{ *term.Term }

func (r realTerm) IsPlain() bool { return r.Plain }

// Terminal implements Deps.
func (e *EngineDeps) Terminal() (Terminal, error) {
	if e.Open != nil {
		return e.Open()
	}
	t, err := term.Open()
	if err != nil {
		return nil, err
	}
	return realTerm{t}, nil
}

// Scan implements Deps: one engine.Plan pass over every category, optional ones that are off included.
func (e *EngineDeps) Scan(ctx context.Context, progress func(category string, done, total int)) ([]Row, error) {
	env, err := e.Build()
	if err != nil {
		return nil, err
	}
	cp := *env
	cp.Ctx, cp.Progress = ctx, progress
	sum := engine.Plan(&cp, e.Cats, engine.Options{ShowOptionalOff: true, Budget: scanBudget})
	if sum.Mode == "error" {
		return nil, errors.New("exclusions are not loaded")
	}
	e.now, e.freeNow = env.Now, sum.FreeBefore
	e.roots = e.roots[:0]
	for _, r := range env.Roots {
		e.roots = append(e.roots, r.Path)
	}
	e.bases = withResolved(cleanBases(append(slices.Clone(env.Platform.ScratchBases()), env.TempDir)))
	e.planEnv, e.plan = cp, sum // kept for the second phase, Uncovered
	return rowsOf(sum), nil
}

// Summary implements Deps.
func (e *EngineDeps) Summary() SummaryData {
	s := SummaryData{Now: e.now, FreeNow: e.freeNow, Roots: slices.Clone(e.roots), Bases: slices.Clone(e.bases)}
	s.Home, _ = os.UserHomeDir()
	if e.now.IsZero() {
		s.Now = time.Now()
	}
	if e.Facts != nil {
		f := e.Facts()
		s.Agents, s.Interval, s.Paused = f.Agents, f.Interval, f.Paused
		if d, err := time.ParseDuration(f.NextIn); err == nil && f.Interval > 0 {
			s.Next = s.Now.Add(d)
		}
	}
	return s
}

// Delete implements Deps: the same engine entry point as `run`, for exactly paths.
func (e *EngineDeps) Delete(ctx context.Context, paths []string) Result {
	env, err := e.Build()
	if err != nil {
		return Result{Err: err.Error()}
	}
	cp := *env
	cp.Ctx = ctx
	sum, err := e.Run(&cp, engine.Options{Explicit: paths, Trigger: "tui", Budget: scanBudget})
	if err != nil {
		return Result{Err: err.Error()}
	}
	return resultOf(sum)
}

// AddExclusion implements Deps.
func (e *EngineDeps) AddExclusion(path string) error { return e.Exclude(path) }

// RemoveExclusion implements Deps.
func (e *EngineDeps) RemoveExclusion(path string) error { return e.Unexclude(path) }

// resultOf turns the engine's summary of a deletion into the result view. A path a guard kept at delete time is
// reported, not an error.
func resultOf(s engine.Summary) Result {
	r := Result{Freed: s.FreedBytes}
	switch s.Mode {
	case "run":
	case "paused":
		r.Err = "paused: run sapulidi resume"
	case "busy":
		r.Err = "another run is in progress: nothing was deleted"
	default:
		r.Err = "nothing was deleted (" + s.Mode + ")"
	}
	for _, c := range s.Removed {
		r.Removed = append(r.Removed, c.Path)
	}
	for _, k := range s.Kept {
		r.Kept = append(r.Kept, KeptItem{Path: k.Path, Why: k.Why})
	}
	for _, k := range s.Failed {
		r.Kept = append(r.Kept, KeptItem{Path: k.Path, Why: "failed"})
	}
	return r
}

// rowsOf turns a plan into list rows. A candidate without a Path (a container, image or builder prune, a worktree
// prune) has no identity to give the engine's Explicit list, so it becomes a dim row that cannot be selected.
func rowsOf(s engine.Summary) []Row {
	var rows []Row
	noOrigin := map[string]bool{}
	ruleSource := map[string]string{} // a Kept row has no source: it takes the one of its rule's candidates
	for _, c := range s.WouldRemove {
		wholesale := c.Path == "" || isWholesale(c)
		if wholesale {
			delete(ruleSource, c.RuleKey)
			noOrigin[c.RuleKey] = true // a wholesale rule's excluded row never becomes selectable
		} else if !noOrigin[c.RuleKey] {
			ruleSource[c.RuleKey] = sourceOf(c.Source)
		}
		switch {
		case wholesale:
			rows = append(rows, wholesaleRow(c))
		default:
			rows = append(rows, Row{Path: c.Path, Bytes: c.Bytes, Created: c.Created, Updated: c.Updated,
				Source: sourceOf(c.Source), Origin: sourceOf(c.Source), Category: c.Category, RuleKey: c.RuleKey, Why: c.Reason})
		}
	}
	for _, k := range s.Kept {
		if k.Path == "" {
			continue
		}
		row := Row{Path: k.Path, Bytes: -1, Source: SrcKept, Category: k.Category, RuleKey: k.RuleKey,
			Why: "kept by a guard: " + k.Why, Guard: k.Why}
		if k.Why == "excluded" {
			row.Source, row.Why = SrcExcluded, excludedReason
			row.Origin = ruleSource[k.RuleKey]
		}
		rows = append(rows, row)
	}
	return rows
}

func sourceOf(s string) string {
	switch s {
	case SrcOptionalOn, SrcOptionalOff:
		return s
	}
	return SrcAuto
}

// containersReason is the dim reason of a container, image or builder prune row: the engine has no path to select.
const containersReason = "removed by the scheduled run or by docker rm / podman rm, never from this screen"

// isWholesale reports a tool command that cleans a whole cache (prune, clean, simctl delete unavailable), whatever
// Path it reports. Only an uninstall of one exact toolchain version is per-item and stays selectable.
func isWholesale(c core.Candidate) bool {
	if c.Removal != core.ToolCommand || len(c.Command) == 0 {
		return false
	}
	// per-item: `rustup toolchain uninstall <name>`; every other shape is wholesale, the safe default
	return !(len(c.Command) >= 4 && c.Command[0] == "rustup" && c.Command[1] == "toolchain" && c.Command[2] == "uninstall")
}

func wholesaleRow(c core.Candidate) Row {
	cmd := strings.Join(c.Command, " ")
	if cmd == "" {
		cmd = c.Reason
	}
	block := WholesaleReason(cmd)
	if c.Category == "containers" {
		block = containersReason
	}
	return Row{Path: cmd, Bytes: c.Bytes, Source: sourceOf(c.Source), Origin: sourceOf(c.Source), Category: c.Category, RuleKey: c.RuleKey, Why: c.Reason,
		Block: block}
}
