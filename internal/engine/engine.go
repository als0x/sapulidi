// Package engine is the only code that removes user data. It takes candidates from categories, runs every
// generic guard and the category's Proof right before each removal, and removes with a rename commit point.
package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/guard"
	"github.com/als0x/sapulidi/internal/state"
)

const defaultBudget = 120 * time.Second

// Options controls one run.
type Options struct {
	DryRun, Quick bool
	Budget        time.Duration
	Trigger       string
	Only          []string // categories
	Paths         []string // when set, only candidates with these exact paths (AG01 clean-once)
	// IgnoreEnabled, only with Paths and Only, runs an optional category that is not enabled; consent and every
	// guard still apply.
	IgnoreEnabled bool
	// ShowOptionalOff, a Plan option (Run ignores it), also lists the candidates of optional categories that are
	// switched off, tagged Source "optional-off".
	ShowOptionalOff bool `json:"-" toml:"-"`
	// Explicit is a list of exact paths the user picked in the terminal UI. For those paths only, the
	// category-enabled state and the per-rule consent are waived; every guard, the never bucket, exclusions, the
	// pause file and the run lock still apply. In-process only: nothing outside engine and tui may set it.
	// Entries that are empty, relative or not filepath.Clean'ed select nothing and are kept as not-a-candidate.
	// It cannot be combined with Paths.
	Explicit []string `json:"-" toml:"-"`
	// IncludeRecommended waives consent, for this run, for candidates whose rule has Recommend set, and waives
	// nothing else. In-process only: nothing outside engine and `run` may set it.
	IncludeRecommended bool `json:"-" toml:"-"`
}

// Summary is the outcome of Plan or Run.
type Summary struct {
	Removed, WouldRemove  []core.Candidate
	Kept, Failed          []core.Kept
	FreedBytes            int64
	FreeBefore, FreeAfter uint64
	Mode                  string // "run" | "dry-run" | "no-consent" | "paused" | "busy" | "uninstalled"
	Stopped               bool
	Waiting               []string // safe and recommended rule keys waiting for consent
}

// Hooks: each is set once in the owner's init(); a second call panics; unset means skip.
var (
	beforeRun func(env *core.Env, st *state.Store, opt Options) string
	afterRun  func(env *core.Env, st *state.Store, s *Summary)
	lowDisk   func(env *core.Env, s *Summary) []core.Candidate

	beforeSet, afterSet, lowSet bool
)

// SetBeforeRun registers the hook that may force a mode (every candidate becomes dry run, Mode is the string).
func SetBeforeRun(fn func(env *core.Env, st *state.Store, opt Options) string) {
	if beforeSet {
		panic("engine: SetBeforeRun called twice")
	}
	beforeSet, beforeRun = true, fn
}

// SetAfterRun registers the hook that sees the finished summary.
func SetAfterRun(fn func(env *core.Env, st *state.Store, s *Summary)) {
	if afterSet {
		panic("engine: SetAfterRun called twice")
	}
	afterSet, afterRun = true, fn
}

// SizeContext is Size that stops, returning -1, when ctx is done.
func SizeContext(ctx context.Context, path string) int64 { return sizeCtx(ctx, path) }

// SetLowDisk registers the hook that returns low-disk candidates in the order to try.
func SetLowDisk(fn func(env *core.Env, s *Summary) []core.Candidate) {
	if lowSet {
		panic("engine: SetLowDisk called twice")
	}
	lowSet, lowDisk = true, fn
}

// factSink implements core.FactSink; Run hands the collected facts to the state store at the end of the run.
type factSink struct {
	mu    sync.Mutex
	facts map[string]string
}

func (f *factSink) Fact(key, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.facts == nil {
		f.facts = map[string]string{}
	}
	f.facts[key] = text
}

var slowCategories = map[string]bool{"worktrees": true, "worktree-prune": true, "containers": true,
	"toolchains": true, "browsers": true, "caches": true, "project": true}

type runner struct {
	env         *core.Env // a copy of the caller's env with the budget context and the fact sink
	opt         Options
	rules       map[string]core.Rule
	cats        map[string]core.Category
	paths       map[string]bool
	explicit    bool            // Options.Explicit is set: the Explicit waivers apply to explicitSet members only
	explicitSet map[string]bool // the valid (absolute, clean) Explicit paths
	showOff     bool            // Plan with ShowOptionalOff
	sum         *Summary
	sink        *factSink
	dry         bool // every candidate is a dry run
	gate        bool // consent gating applies (false before setup: everything is dry anyway)
	recorded    bool // a consent record exists; without one only IncludeRecommended allows a removal
	waiting     map[string]bool
	logged      bool

	paused    func() bool // reads the pause file; nil when there is no store (Plan)
	pauseAt   time.Time
	pauseSeen bool
}

func newRunner(env *core.Env, opt Options, ctx context.Context, sum *Summary) *runner {
	e := *env
	e.Ctx = ctx
	sink := &factSink{}
	e.Facts = sink
	r := &runner{env: &e, opt: opt, sum: sum, sink: sink,
		rules: map[string]core.Rule{}, cats: map[string]core.Category{}, waiting: map[string]bool{}}
	for _, ru := range env.Rules {
		r.rules[ru.Key()] = ru
	}
	if len(opt.Paths) > 0 {
		r.paths = map[string]bool{}
		for _, p := range opt.Paths {
			r.paths[p] = true
		}
	}
	if len(opt.Explicit) > 0 {
		r.explicit = true
		r.explicitSet = map[string]bool{}
		for _, p := range opt.Explicit {
			// Only an absolute, clean path selects anything: "" would match every path-less candidate.
			if p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p {
				r.explicitSet[p] = true
			}
		}
	}
	return r
}

// refuse says why a run or plan must not start at all, or "".
func refuse(env *core.Env, opt Options) string {
	if env.Exclusions == nil {
		return "exclusions-not-loaded" // never run without knowing what the user protects
	}
	if len(opt.Paths) > 0 && len(opt.Explicit) > 0 {
		return "paths-and-explicit"
	}
	return ""
}

func (r *runner) event(kind string, fields map[string]any) {
	if r.env.Log != nil {
		r.env.Log.Event(kind, fields)
	}
}

// pauseCheckEvery is the least time between two reads of the pause file during a run.
var pauseCheckEvery = 2 * time.Second

func (r *runner) stopped() bool {
	if r.env.Ctx.Err() != nil {
		r.sum.Stopped = true
		if !r.logged {
			r.logged = true
			r.event("budget", map[string]any{"trigger": r.opt.Trigger})
		}
		return true
	}
	// `sapulidi pause` stops every run, a running one included: a cheap stat, at most once per pauseCheckEvery.
	if r.paused != nil && !r.pauseSeen && (r.pauseAt.IsZero() || time.Since(r.pauseAt) >= pauseCheckEvery) {
		r.pauseAt = time.Now()
		r.pauseSeen = r.paused()
		if r.pauseSeen {
			r.event("paused", map[string]any{"trigger": r.opt.Trigger})
		}
	}
	if r.pauseSeen {
		r.sum.Stopped = true
		return true
	}
	return false
}

func contains(list []string, s string) bool { return slices.Contains(list, s) }

// scan runs the categories in order and handles their candidates.
func (r *runner) scan(cats []core.Category) {
	if r.explicit && len(r.explicitSet) == 0 {
		return // nothing valid was selected: scan nothing
	}
	for _, cat := range cats {
		r.cats[cat.Name()] = cat
	}
	for i, cat := range cats {
		if r.stopped() {
			return
		}
		if !r.scanOne(cat) {
			return
		}
		if r.env.Progress != nil {
			r.env.Progress(cat.Name(), i+1, len(cats))
		}
	}
}

// scanOne scans one category and handles its candidates; false means the run was stopped.
func (r *runner) scanOne(cat core.Category) bool {
	name := cat.Name()
	if len(r.opt.Only) > 0 && !contains(r.opt.Only, name) {
		return true
	}
	if r.opt.Quick && slowCategories[name] {
		return true
	}
	if cat.Bucket() == core.Never {
		return true
	}
	if !r.env.Settings.CategoryEnabled(name) {
		ignore := r.opt.IgnoreEnabled && len(r.opt.Paths) > 0 && contains(r.opt.Only, name) && cat.Bucket() == core.Optional
		ignore = ignore || r.explicit || (r.showOff && cat.Bucket() == core.Optional)
		if !ignore {
			return true
		}
	}
	cands, kept, err := safeScan(cat, r.env)
	if r.stopped() {
		return false
	}
	r.sum.Kept = append(r.sum.Kept, kept...)
	if err != nil {
		r.sum.Failed = append(r.sum.Failed, core.Kept{Category: name, Why: "error"})
		r.event("scan-failed", map[string]any{"category": name, "error": err.Error()})
		return true
	}
	for _, c := range cands {
		if r.stopped() {
			return false
		}
		r.handle(cat, c, false)
	}
	return true
}

// sourceOf says where a candidate comes from: an auto category or rule, or an enabled optional category.
func (r *runner) sourceOf(cat core.Category, rule core.Rule) string {
	if cat.Bucket() == core.Auto || rule.Bucket == core.Auto {
		return "auto"
	}
	if !r.env.Settings.CategoryEnabled(cat.Name()) {
		return "optional-off"
	}
	return "optional-on"
}

// labelOf is the label of a candidate of this rule.
func labelOf(rule core.Rule) core.Label {
	switch {
	case rule.Bucket == core.Auto:
		return core.LabelSafe
	case rule.Recommend:
		return core.LabelRecommended
	}
	return core.LabelYourCall
}

// consented says whether this run may remove a candidate of the rule without the user picking it: the rule key is in
// the consent record, or the run includes recommended rows and this is one. Without a record nothing is consented.
func (r *runner) consented(rule core.Rule) bool {
	if r.opt.IncludeRecommended && rule.Recommend {
		return true
	}
	return r.recorded && r.env.Settings.Consented(rule.Key())
}

// handle decides what happens to one candidate. lowDisk candidates skip the Paths filter; consent and every guard
// still apply to them.
func (r *runner) handle(cat core.Category, c core.Candidate, lowDisk bool) {
	if !r.env.Settings.RuleEnabled(c.RuleKey) {
		return
	}
	if r.explicit {
		// A path-less candidate (worktree prune) is not a user-selectable path.
		if c.Path == "" || !r.explicitSet[c.Path] {
			return
		}
	} else if !lowDisk && r.paths != nil && !r.paths[c.Path] {
		return
	}
	if c.Removal == core.ReportOnly {
		return
	}
	// Every kind needs a known rule of the category that produced the candidate, before any guard or command.
	rule, haveRule := r.rules[c.RuleKey]
	if !haveRule || rule.Category != cat.Name() || c.Category != cat.Name() {
		r.sum.Kept = append(r.sum.Kept, core.Kept{Category: c.Category, RuleKey: c.RuleKey, Path: c.Path, Why: "error"})
		r.event("keep", map[string]any{"path": c.Path, "why": "error", "detail": "no rule of category " + cat.Name() + ": " + c.RuleKey})
		return
	}
	// The stricter of the rule's and the category's bucket wins.
	if rule.Bucket == core.Never || cat.Bucket() == core.Never {
		return
	}
	c.Bytes = -1
	c.Label = labelOf(rule)
	if err := r.guards(cat, c, rule); err != nil {
		r.sum.Kept = append(r.sum.Kept, keptOf(c, err))
		return
	}
	if !r.opt.Quick && c.Path != "" {
		c.Source = r.sourceOf(cat, rule)
		m, err := guard.Measure(r.env.Ctx, c.Path)
		c.Created, c.Updated = m.Born, m.Newest
		if err == nil {
			c.Bytes = m.Bytes
		}
	}
	if r.stopped() {
		r.sum.Kept = append(r.sum.Kept, core.Kept{Category: c.Category, RuleKey: c.RuleKey, Path: c.Path, Why: "budget"})
		return
	}
	dry := r.dry
	if r.gate && !r.explicit && !r.consented(rule) {
		if c.Label != core.LabelYourCall { // waiting is what setup and consent --yes can confirm
			r.waiting[c.RuleKey] = true
		}
		dry = true
	}
	if dry {
		r.sum.WouldRemove = append(r.sum.WouldRemove, c)
		return
	}
	if err := r.safeRemove(cat, c, rule); err != nil {
		var ke *core.KeepError
		if errors.As(err, &ke) { // a guard failed on the re-check right before the commit point
			r.sum.Kept = append(r.sum.Kept, keptOf(c, err))
			return
		}
		r.sum.Failed = append(r.sum.Failed, core.Kept{Category: c.Category, RuleKey: c.RuleKey, Path: c.Path, Why: "error"})
		r.event("remove-failed", map[string]any{"path": c.Path, "error": err.Error()})
		return
	}
	r.callAfterRemove(cat, c)
	r.sum.Removed = append(r.sum.Removed, c)
	if c.Bytes > 0 {
		r.sum.FreedBytes += c.Bytes
	}
}

// safeScan turns a panic in a category's Scan into an error, so one broken category cannot end the run.
func safeScan(cat core.Category, env *core.Env) (cands []core.Candidate, kept []core.Kept, err error) {
	defer func() {
		if p := recover(); p != nil {
			cands, kept, err = nil, nil, fmt.Errorf("scan panicked: %v", p)
		}
	}()
	return cat.Scan(env)
}

// callBefore runs the BeforeRun hook; a panic forces Mode "error", which is a dry run.
func (r *runner) callBefore(st *state.Store, opt Options) (mode string) {
	defer func() {
		if p := recover(); p != nil {
			r.event("hook-panic", map[string]any{"hook": "before-run", "panic": fmt.Sprint(p)})
			mode = "error"
		}
	}()
	return beforeRun(r.env, st, opt)
}

// callLowDisk runs the LowDisk hook; a panic is logged and gives no candidates.
func (r *runner) callLowDisk(s *Summary) (cands []core.Candidate) {
	defer func() {
		if p := recover(); p != nil {
			r.event("hook-panic", map[string]any{"hook": "low-disk", "panic": fmt.Sprint(p)})
			cands = nil
		}
	}()
	return lowDisk(r.env, s)
}

// callAfter runs the AfterRun hook; a panic is logged and ignored.
func (r *runner) callAfter(st *state.Store, s *Summary) {
	defer func() {
		if p := recover(); p != nil {
			r.event("hook-panic", map[string]any{"hook": "after-run", "panic": fmt.Sprint(p)})
		}
	}()
	afterRun(r.env, st, s)
}

// safeRemove turns a panic during a removal into an error for that candidate.
func (r *runner) safeRemove(cat core.Category, c core.Candidate, rule core.Rule) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("removal panicked: %v", p)
		}
	}()
	return r.remove(cat, c, rule)
}

// callAfterRemove tells a category that implements core.AfterRemover that a removal went through. It is reached
// only for a real (non-dry) removal without error; a panic is logged and ignored.
func (r *runner) callAfterRemove(cat core.Category, c core.Candidate) {
	ar, ok := cat.(core.AfterRemover)
	if !ok {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			r.event("hook-panic", map[string]any{"hook": "after-remove", "panic": fmt.Sprint(p)})
		}
	}()
	ar.AfterRemove(r.env, c)
}

// safeProof turns a panic in a category's Proof into a keep.
func safeProof(cat core.Category, env *core.Env, c core.Candidate) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = core.Keep("error", fmt.Sprintf("proof panicked: %v", p))
		}
	}()
	return cat.Proof(env, c)
}

func keptOf(c core.Candidate, err error) core.Kept {
	why := "error"
	var ke *core.KeepError
	if errors.As(err, &ke) {
		why = ke.Why
	}
	return core.Kept{Category: c.Category, RuleKey: c.RuleKey, Path: c.Path, Why: why}
}

func (r *runner) freeBytes() uint64 {
	n, err := r.env.Platform.FreeBytes(r.env.Home)
	if err != nil {
		return 0
	}
	return n
}

// reportMissing keeps every Explicit path that no category turned into a candidate or a keep.
func (r *runner) reportMissing(s *Summary) {
	if !r.explicit {
		return
	}
	why := "not-a-candidate"
	if s.Stopped { // the scan did not finish: an unseen path may still have been a candidate
		why = "budget"
		if r.pauseSeen {
			why = "paused"
		}
	}
	seen := map[string]bool{}
	for _, c := range s.Removed {
		seen[c.Path] = true
	}
	for _, c := range s.WouldRemove {
		seen[c.Path] = true
	}
	for _, ks := range [][]core.Kept{s.Kept, s.Failed} {
		for _, k := range ks {
			seen[k.Path] = true
		}
	}
	done := map[string]bool{}
	for _, p := range r.opt.Explicit {
		if done[p] {
			continue
		}
		done[p] = true
		switch {
		case !r.explicitSet[p]: // empty, relative or unclean: it never selected anything
			s.Kept = append(s.Kept, core.Kept{Path: p, Why: "not-a-candidate"})
		case !seen[p]:
			s.Kept = append(s.Kept, core.Kept{Path: p, Why: why})
		}
	}
}

func (r *runner) finishSummary(s *Summary) {
	r.reportMissing(s)
	for k := range r.waiting {
		s.Waiting = append(s.Waiting, k)
	}
	sort.Strings(s.Waiting)
}

// Plan reports what a run would do. It never removes anything.
func Plan(env *core.Env, cats []core.Category, opt Options) Summary {
	if refuse(env, opt) != "" {
		return Summary{Mode: "error"}
	}
	ctx, cancel := budgetCtx(env, opt)
	defer cancel()
	s := Summary{Mode: "dry-run"}
	r := newRunner(env, opt, ctx, &s)
	r.dry, r.gate, r.recorded = true, true, true
	r.showOff = opt.ShowOptionalOff
	s.FreeBefore = r.freeBytes()
	r.scan(cats)
	s.FreeAfter = s.FreeBefore
	r.finishSummary(&s)
	return s
}

func budgetCtx(env *core.Env, opt Options) (context.Context, context.CancelFunc) {
	base := env.Ctx
	if base == nil {
		base = context.Background()
	}
	b := opt.Budget
	if b <= 0 {
		b = defaultBudget
	}
	return context.WithTimeout(base, b)
}

// Run plans and removes.
func Run(env *core.Env, cats []core.Category, st *state.Store, opt Options) (Summary, error) {
	opt.ShowOptionalOff = false // a plan option only
	if refuse(env, opt) != "" {
		return Summary{Mode: "error"}, nil
	}
	if len(opt.Explicit) > 0 {
		opt.Trigger = "tui"
	}
	if st.Paused() {
		return Summary{Mode: "paused"}, nil
	}
	release, err := st.Lock()
	if errors.Is(err, state.ErrBusy) {
		return Summary{Mode: "busy"}, nil
	}
	if err != nil {
		return Summary{}, err
	}
	defer release()

	ctx, cancel := budgetCtx(env, opt)
	defer cancel()
	s := Summary{Mode: "run"}
	r := newRunner(env, opt, ctx, &s)
	r.paused = st.Paused
	if r.env.Log == nil {
		r.env.Log = st.Logger()
	}

	forced := ""
	if beforeRun != nil {
		forced = r.callBefore(st, opt)
	}
	consent, cerr := st.Consent()
	if cerr != nil {
		r.event("consent-unreadable", map[string]any{"error": cerr.Error()})
	}
	switch {
	case forced != "":
		s.Mode, r.dry = forced, true
	case consent == nil && !r.explicit && !opt.IncludeRecommended:
		s.Mode, r.dry = "no-consent", true
	case opt.DryRun:
		s.Mode, r.dry = "dry-run", true
	}
	r.recorded = consent != nil
	r.gate = r.recorded || opt.IncludeRecommended

	s.FreeBefore = r.freeBytes()
	if !r.dry {
		for _, k := range finish(r.env, finishTargets(r.env, st)) {
			s.Kept = append(s.Kept, k)
		}
	}
	r.scan(cats)

	s.FreeAfter = r.freeBytes()
	limit := r.env.Settings.LowDiskBytes()
	// The low-disk tier runs only in a full run: a clean-once or filtered run removes only what was asked for.
	// A quick run skips the hook altogether: its candidates come from slow categories and sizing them is slow.
	if !r.dry && !opt.Quick && !s.Stopped && lowDisk != nil && limit > 0 && s.FreeAfter < limit &&
		len(opt.Paths) == 0 && len(opt.Only) == 0 && !r.explicit {
		for _, c := range r.callLowDisk(&s) {
			if s.FreeAfter >= limit || r.stopped() {
				break
			}
			cat, ok := r.cats[c.Category]
			if !ok || cat.Bucket() == core.Never || !r.env.Settings.CategoryEnabled(c.Category) {
				continue // from a disabled, never or unknown category: skipped silently
			}
			if opt.Quick && slowCategories[c.Category] {
				continue
			}
			r.handle(cat, c, true)
			s.FreeAfter = r.freeBytes()
		}
	}
	if limit > 0 && s.FreeAfter > 0 && s.FreeAfter < limit {
		r.sink.Fact("low-disk", fmt.Sprintf("Free disk space is below %d GB.", limit/(1<<30)))
	}
	r.finishSummary(&s)
	if len(s.Waiting) > 0 {
		r.sink.Fact("waiting-rules", fmt.Sprintf("%d rule(s) wait for your confirmation: run setup.", len(s.Waiting)))
	}
	if !r.explicit { // an Explicit run shows no notification and does not use up the notify-once flag
		r.flushFacts(st)
	}

	if afterRun != nil {
		r.callAfter(st, &s)
	}
	st.RunRecord(s)
	fields := map[string]any{"mode": s.Mode, "trigger": opt.Trigger, "removed": len(s.Removed),
		"would_remove": len(s.WouldRemove), "kept": len(s.Kept), "failed": len(s.Failed),
		"freed_bytes": s.FreedBytes, "stopped": s.Stopped}
	if r.explicit {
		fields["explicit"] = len(opt.Explicit)
	}
	r.event("summary", fields)
	return s, nil
}

// flushFacts records every fact; a new or changed one is notified, and a failed notification is remembered.
func (r *runner) flushFacts(st *state.Store) {
	if r.dry {
		return // a dry run neither notifies nor uses up the notify-once flag
	}
	r.sink.mu.Lock()
	facts := r.sink.facts
	r.sink.mu.Unlock()
	keys := make([]string, 0, len(facts))
	for k := range facts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !st.Fact(k, facts[k], false) || !r.env.Settings.NotifyEnabled() {
			continue
		}
		if err := r.env.Platform.Notify("sapulidi", facts[k]); err != nil {
			st.MarkUndelivered(k)
		}
	}
}
