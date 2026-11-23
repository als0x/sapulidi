package cli

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/exclude"
	"github.com/als0x/sapulidi/internal/execx"
	packsload "github.com/als0x/sapulidi/internal/packs"
	"github.com/als0x/sapulidi/internal/platform"
	"github.com/als0x/sapulidi/internal/roots"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/packs"
)

func init() { BuildEnv = buildEnv }

// OpenStore opens the state directory of this installation.
func OpenStore() (*state.Store, error) {
	p, err := config.ResolvePaths()
	if err != nil {
		return nil, err
	}
	return state.Open(p)
}

// loadRules loads the built-in packs and the user packs next to the config file. A pack with errors is ignored
// as a whole; its errors are written to the log by the caller that has a store.
func loadRules(p config.Paths) ([]core.Rule, []error) {
	return packsload.Load(packs.FS, filepath.Join(filepath.Dir(p.Config), "packs"))
}

// autoRuleKeys returns the sorted keys of every auto rule.
func autoRuleKeys(rules []core.Rule) []string {
	var keys []string
	for _, r := range rules {
		if r.Bucket == core.Auto {
			keys = append(keys, r.Key())
		}
	}
	sort.Strings(keys)
	return keys
}

// waitingRuleKeys returns the auto rule keys the consent record does not list.
func waitingRuleKeys(rules []core.Rule, consent *config.Consent) []string {
	have := map[string]bool{}
	if consent != nil {
		for _, k := range consent.Rules {
			have[k] = true
		}
	}
	var out []string
	for _, k := range autoRuleKeys(rules) {
		if !have[k] {
			out = append(out, k)
		}
	}
	return out
}

// buildEnv resolves paths, loads config, consent and packs, discovers the roots and builds the Env every command
// that scans needs. `check` does not use it: it must not scan.
func buildEnv() (*core.Env, error) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return nil, err
	}
	cfg, warns, err := config.Load(paths.Config)
	if err != nil {
		return nil, err
	}
	// A corrupt exclude.toml fails the command closed: nothing may run without knowing what the user protects.
	excl, err := exclude.Load(paths)
	if err != nil {
		engine.SetExclusions(nil)
		return nil, err
	}
	engine.SetExclusions(excl)
	st, err := state.Open(paths)
	if err != nil {
		return nil, err
	}
	log := st.Logger()
	for _, w := range warns {
		log.Event("config-warning", map[string]any{"warning": w})
	}
	consent, err := st.Consent()
	if err != nil {
		log.Event("consent-unreadable", map[string]any{"error": err.Error()})
		consent = nil // fail closed: an unreadable consent record is no consent
	}
	rules, perrs := loadRules(paths)
	for _, e := range perrs {
		log.Event("pack-error", map[string]any{"error": e.Error()})
	}
	cats := core.Categories()
	buckets := make(map[string]core.Bucket, len(cats))
	for _, c := range cats {
		buckets[c.Name()] = c.Bucket()
	}
	for _, w := range cfg.AgeWarnings(buckets) {
		log.Event("config-warning", map[string]any{"warning": w})
	}

	run := execx.Real{}
	plat := platform.New(run)
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	tempDir, _ := plat.TempDir() // "" when unknown: the temp category then has nothing to scan
	now := time.Now()
	found, _ := roots.Discover(roots.DiscoverEnv{
		Home: home, Config: cfg, Platform: plat, Run: run, Now: now,
		TempDirs: []string{tempDir, "/tmp", "/private/tmp"},
	})
	return &core.Env{
		Home: home, Now: now, Roots: found, Rules: rules, Platform: plat, Run: run,
		Settings: config.NewSettings(cfg, consent, buckets), Log: log, Facts: nopFacts{},
		Ctx: context.Background(), TempDir: tempDir,
	}, nil
}
