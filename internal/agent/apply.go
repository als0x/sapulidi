package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	packsload "github.com/als0x/sapulidi/internal/packs"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/packs"
)

// ApplyDeps are what applying a proposal needs. Env, Cats and Store are used by clean-once only.
type ApplyDeps struct {
	Paths config.Paths
	Yes   bool // the chat tier: the user said yes (spec S15)
	Env   *core.Env
	Cats  []core.Category
	Store *state.Store
}

// ApplyResult says what applying did.
type ApplyResult struct {
	Message      string
	Removed      []string
	FreedBytes   int64
	Kept         []core.Kept
	NotCandidate []string // listed paths that no category scan produced; left alone
}

var errNeedYes = errors.New("this changes your setup: run it again with --yes after you agreed")

// Apply carries out one proposal. The proposal is validated again first: the file may have been edited. Config
// changes go through config.Apply, a rule becomes a user pack after the terminal tier, and clean-once runs the
// engine, so every guard still decides (spec S1, S14, S15). Approving a proposal is also the user's consent for the
// rules it enables; that is recorded only after a terminal confirmation, never on --yes alone.
func Apply(d ApplyDeps, p Proposal) (ApplyResult, error) {
	if err := Validate(p); err != nil {
		return ApplyResult{}, fmt.Errorf("proposal %s is not valid: %w", p.ID, err)
	}
	switch p.Kind {
	case KindEnable, KindDisable:
		if !d.Yes {
			return ApplyResult{}, errNeedYes
		}
		v := "true"
		if p.Kind == KindDisable {
			v = "false"
		}
		res, err := changed(d, config.Change{Key: "category." + p.Target + ".enabled", Value: v})
		if err != nil || p.Kind == KindDisable {
			return res, err
		}
		return allowCategory(d, p, res)
	case KindSetAge:
		if !d.Yes {
			return ApplyResult{}, errNeedYes
		}
		return changed(d, config.Change{Key: "category." + p.Target + ".age", Value: p.Value})
	case KindAddRule:
		return addRule(d, p)
	case KindCleanOnce:
		if !d.Yes {
			return ApplyResult{}, errNeedYes
		}
		return cleanOnce(d, p)
	}
	return ApplyResult{}, fmt.Errorf("unknown kind %q", p.Kind)
}

func changed(d ApplyDeps, c config.Change) (ApplyResult, error) {
	if err := config.Apply(d.Paths, c); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Message: fmt.Sprintf("set %s = %s (in %s)", c.Key, c.Value, config.AppliedPath(d.Paths.Config))}, nil
}

// addRule is the terminal tier: the user retypes the rule id on /dev/tty. --yes never replaces that.
func addRule(d ApplyDeps, p Proposal) (ApplyResult, error) {
	text, err := RuleFile(p)
	if err != nil {
		return ApplyResult{}, err
	}
	// The user sees exactly what is written (names, markers, bucket, age) before the question.
	prompt := fmt.Sprintf("Proposal %s adds this rule as a user pack:\n\n%s\n"+
		"Scheduled runs may then remove what it finds. Type the rule id (%s) to confirm: ", p.ID, text, p.Target)
	if err := confirm.Terminal(prompt, p.Target); err != nil {
		return ApplyResult{}, err
	}
	dir := filepath.Join(filepath.Dir(d.Paths.Config), "packs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ApplyResult{}, err
	}
	path := filepath.Join(dir, PackName(p.ID)+".toml")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("cannot create %s: %w", path, err)
	}
	if _, err := f.Write(text); err != nil {
		f.Close()
		return ApplyResult{}, err
	}
	if err := f.Close(); err != nil {
		return ApplyResult{}, err
	}
	// As RuleFile renders it and the loader names a user pack.
	key := core.Rule{Pack: "user-" + PackName(p.ID), ID: p.Target, Rev: 1}.Key()
	recorded, err := recordConsent(d.Paths, []string{key})
	if err != nil {
		return ApplyResult{}, fmt.Errorf("the rule is in %s, but your consent was not recorded: %w", path, err)
	}
	if !recorded {
		return ApplyResult{Message: "added the rule in " + path + "; run sapulidi setup before it can run"}, nil
	}
	return ApplyResult{Message: "added the rule in " + path + " and recorded your consent for " + key}, nil
}

// allowCategory asks, on the terminal, whether scheduled runs may also remove what the rules of the category just
// switched on find. Without that confirmation the category stays on and nothing is recorded.
func allowCategory(d ApplyDeps, p Proposal, res ApplyResult) (ApplyResult, error) {
	consent, err := consentRecord(d.Paths)
	if err != nil || consent == nil {
		return res, err
	}
	rules, _ := packsload.Load(packs.FS, filepath.Join(filepath.Dir(d.Paths.Config), "packs"))
	var keys []string
	for _, r := range rules {
		if r.Category == p.Target && r.Bucket != core.Never && !slices.Contains(consent.Rules, r.Key()) {
			keys = append(keys, r.Key())
		}
	}
	if len(keys) == 0 {
		return res, nil
	}
	slices.Sort(keys)
	later := "; scheduled runs still need your OK: sapulidi consent --yes " + strings.Join(keys, " ")
	prompt := fmt.Sprintf("Category %s is on. Scheduled runs may also remove what these rules find:\n  %s\n"+
		"Type allow to confirm: ", p.Target, strings.Join(keys, "\n  "))
	err = confirm.Terminal(prompt, "allow")
	if errors.Is(err, confirm.ErrNoTerminal) || errors.Is(err, confirm.ErrMismatch) {
		res.Message += later
		return res, nil
	}
	if err != nil {
		return res, err
	}
	if _, err := recordConsent(d.Paths, keys); err != nil {
		return res, err
	}
	res.Message += fmt.Sprintf("; recorded your consent for %d rules", len(keys))
	return res, nil
}

// consentRecord reads the consent record; nil means setup never ran.
func consentRecord(paths config.Paths) (*state.Consent, error) {
	st, err := state.Open(paths)
	if err != nil {
		return nil, err
	}
	return st.Consent()
}

// recordConsent adds keys to the consent record. It never creates the record (false: setup never ran) and refuses
// while a config file is corrupt.
func recordConsent(paths config.Paths, keys []string) (bool, error) {
	cfg, _, err := config.Load(paths.Config)
	if err != nil {
		return false, err
	}
	if len(cfg.Invalid) > 0 {
		return false, fmt.Errorf("%s is not valid TOML", cfg.Invalid[0])
	}
	st, err := state.Open(paths)
	if err != nil {
		return false, err
	}
	// Read here, after any confirmation: it can block for a long time.
	consent, err := st.Consent()
	if err != nil || consent == nil {
		return false, err
	}
	all := append(slices.Clone(consent.Rules), keys...)
	slices.Sort(all)
	if err := st.SaveConsent(&state.Consent{At: time.Now().UTC(), Rules: slices.Compact(all)}); err != nil {
		return false, err
	}
	st.Logger().Event("consent", map[string]any{"rules": keys})
	return true, nil
}

// allowed is core.Settings plus the rule keys the user consented to after the Env was built.
type allowed struct {
	core.Settings
	keys []string
}

func (a allowed) Consented(key string) bool {
	return slices.Contains(a.keys, key) || a.Settings.Consented(key)
}

// allowCleanOnce makes sure the rules of the listed paths are consented: for those that are not, the user confirms on
// the terminal and the consent is recorded. It returns the Env the run must use.
func allowCleanOnce(d ApplyDeps, p Proposal, opt engine.Options) (*core.Env, error) {
	consent, err := d.Store.Consent()
	if err != nil || consent == nil || d.Store.Paused() {
		return d.Env, nil // the run itself reports these and removes nothing
	}
	var keys []string
	var lines strings.Builder
	for _, c := range engine.Plan(d.Env, d.Cats, opt).WouldRemove {
		if !d.Env.Settings.Consented(c.RuleKey) {
			keys = append(keys, c.RuleKey)
			fmt.Fprintf(&lines, "  %q  (rule %s)\n", c.Path, c.RuleKey)
		}
	}
	if len(keys) == 0 {
		return d.Env, nil
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	prompt := fmt.Sprintf("Proposal %s removes these paths:\n\n%s\nIt also lets scheduled runs remove what these rules "+
		"find from now on: %s\nType allow to confirm: ", p.ID, lines.String(), strings.Join(keys, ", "))
	if err := confirm.Terminal(prompt, "allow"); err != nil {
		return nil, err
	}
	if _, err := recordConsent(d.Paths, keys); err != nil {
		return nil, err
	}
	env := *d.Env
	env.Settings = allowed{env.Settings, keys}
	return &env, nil
}

// cleanOnce runs the engine for the category with only the listed paths and the category enabled for this run
// only. Consent, the category's own Scan and every guard still apply; a listed path the scan does not produce is
// reported and left alone.
func cleanOnce(d ApplyDeps, p Proposal) (ApplyResult, error) {
	if d.Env == nil || d.Store == nil {
		return ApplyResult{}, errors.New("clean-once needs the environment and the state store")
	}
	opt := engine.Options{Trigger: "proposal", Only: []string{p.Target}, Paths: p.Paths, IgnoreEnabled: true}
	env, err := allowCleanOnce(d, p, opt)
	if err != nil {
		return ApplyResult{}, err
	}
	sum, err := engine.Run(env, d.Cats, d.Store, opt)
	if err != nil {
		return ApplyResult{}, err
	}
	if sum.Mode != "run" {
		return ApplyResult{}, fmt.Errorf("nothing was removed: the run mode is %q (set up consent, resume or retry later)", sum.Mode)
	}
	res := ApplyResult{FreedBytes: sum.FreedBytes}
	listed := map[string]bool{}
	for _, path := range p.Paths {
		listed[path] = true
	}
	seen := map[string]bool{}
	for _, c := range sum.Removed {
		res.Removed = append(res.Removed, c.Path)
		seen[c.Path] = true
	}
	for _, c := range sum.WouldRemove {
		seen[c.Path] = true
	}
	for _, k := range append(append([]core.Kept(nil), sum.Kept...), sum.Failed...) {
		if listed[k.Path] {
			res.Kept = append(res.Kept, k)
			seen[k.Path] = true
		}
	}
	for _, path := range p.Paths {
		if !seen[path] {
			res.NotCandidate = append(res.NotCandidate, path)
		}
	}
	res.Message = fmt.Sprintf("removed %d of %d listed paths", len(res.Removed), len(p.Paths))
	return res, nil
}
