package engine

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/guard"
	"github.com/als0x/sapulidi/internal/state"
)

// target is a directory to look in and the allowed base it must stay under.
type target struct{ dir, base string }

// Finish completes `.sapulidi-removing-*` entries directly under the given bases. Each entry goes through
// Deny and Place against its base first; a symlink is never followed. It returns what it left alone.
func Finish(env *core.Env, bases []string) []core.Kept {
	var ts []target
	for _, b := range bases {
		ts = append(ts, target{b, b})
	}
	return finish(env, ts)
}

// finishTargets are the bases a run looks in: the temp dir, the roots, the scratch bases, and the parents of
// remove-start events in the log that have no matching remove.
func finishTargets(env *core.Env, st *state.Store) []target {
	var ts []target
	add := func(p string) {
		if p != "" {
			ts = append(ts, target{p, p})
		}
	}
	add(env.TempDir)
	for _, r := range env.Roots {
		add(r.Path)
	}
	if env.Platform != nil {
		for _, b := range env.Platform.ScratchBases() {
			add(b)
		}
	}
	return append(ts, unfinishedFromLog(filepath.Dir(st.ProposalsDir()))...)
}

// unfinishedFromLog reads the log (the state dir holds log.jsonl.1 and log.jsonl) and returns the parents of
// remove-start events that were never followed by remove or finish.
func unfinishedFromLog(stateDir string) []target {
	open := map[string]target{} // renamed → where it lives
	var order []string
	for _, name := range []string{"log.jsonl.1", "log.jsonl"} {
		f, err := os.Open(filepath.Join(stateDir, name))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<16), 1<<20)
		for sc.Scan() {
			var ev struct {
				Kind    string `json:"kind"`
				Renamed string `json:"renamed"`
				Parent  string `json:"parent"`
				Base    string `json:"base"`
			}
			if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Renamed == "" {
				continue
			}
			switch ev.Kind {
			case "remove-start":
				if _, ok := open[ev.Renamed]; !ok {
					order = append(order, ev.Renamed)
				}
				open[ev.Renamed] = target{ev.Parent, ev.Base}
			case "remove", "finish":
				delete(open, ev.Renamed)
			}
		}
		_ = f.Close()
	}
	var ts []target
	for _, k := range order {
		if t, ok := open[k]; ok {
			ts = append(ts, t)
		}
	}
	return ts
}

func finish(env *core.Env, ts []target) []core.Kept {
	var kept []core.Kept
	seen := map[target]bool{}
	for _, t := range ts {
		if seen[t] || t.dir == "" || t.base == "" {
			continue
		}
		seen[t] = true
		if baseAllowed(env, t.base) != nil {
			continue
		}
		entries, err := os.ReadDir(t.dir)
		if err != nil {
			continue
		}
		for _, de := range entries {
			if env.Ctx != nil && env.Ctx.Err() != nil {
				return kept
			}
			if !strings.HasPrefix(de.Name(), removingPrefix) {
				continue
			}
			p := filepath.Join(t.dir, de.Name())
			if err := guard.Deny(env.Home, env.TempDir, p); err != nil {
				kept = append(kept, keptOf(core.Candidate{Path: p}, err))
				continue
			}
			if err := guard.Place(env.Platform, t.base, p); err != nil {
				kept = append(kept, keptOf(core.Candidate{Path: p}, err))
				continue
			}
			if err := os.RemoveAll(p); err != nil {
				kept = append(kept, core.Kept{Path: p, Why: "error"})
				continue
			}
			if env.Log != nil {
				env.Log.Event("finish", map[string]any{"renamed": p})
			}
		}
	}
	return kept
}
