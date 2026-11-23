package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/state"
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
// engine, so every guard still decides (spec S1, S14, S15).
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
		return changed(d, config.Change{Key: "category." + p.Target + ".enabled", Value: v})
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
	prompt := fmt.Sprintf("Proposal %s adds this rule as a user pack:\n\n%s\nType the rule id (%s) to confirm: ", p.ID, text, p.Target)
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
	return ApplyResult{Message: "added the rule in " + path + "; a new rule waits for your OK like any other"}, nil
}

// cleanOnce runs the engine for the category with only the listed paths and the category enabled for this run
// only. Consent, the category's own Scan and every guard still apply; a listed path the scan does not produce is
// reported and left alone.
func cleanOnce(d ApplyDeps, p Proposal) (ApplyResult, error) {
	if d.Env == nil || d.Store == nil {
		return ApplyResult{}, errors.New("clean-once needs the environment and the state store")
	}
	sum, err := engine.Run(d.Env, d.Cats, d.Store, engine.Options{
		Trigger: "proposal", Only: []string{p.Target}, Paths: p.Paths, IgnoreEnabled: true,
	})
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
