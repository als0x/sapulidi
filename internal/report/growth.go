package report

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/state"
)

const (
	sampleEvery   = 24 * time.Hour
	growthWindow  = 7 * 24 * time.Hour
	newUnknownMin = 2 << 30
	sampleTop     = 50
	catPrefix     = "cat:" // sample keys that are category totals, not paths
)

func init() { engine.SetAfterRun(afterRun) }

// sampleMin: directories below this are not worth a sample line (a variable for tests).
var sampleMin int64 = 256 << 20

// sample is one line of growth.jsonl, as state.AddGrowthSample writes it.
type sample struct {
	T     time.Time        `json:"t"`
	Sizes map[string]int64 `json:"sizes"`
}

func readSamples(st *state.Store) []sample {
	f, err := os.Open(filepath.Join(filepath.Dir(st.ProposalsDir()), "growth.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []sample
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		var s sample
		if json.Unmarshal(sc.Bytes(), &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

func isPath(key string) bool { return !strings.HasPrefix(key, catPrefix) }

// growthOf is, per directory, its size in the newest sample of the last 7 days minus its size in the oldest one
// that knows it; only growing directories, largest growth first.
func growthOf(samples []sample, now time.Time) []DirGrowth {
	first, last := map[string]int64{}, map[string]int64{}
	for _, s := range samples {
		if now.Sub(s.T) > growthWindow {
			continue
		}
		for k, v := range s.Sizes {
			if !isPath(k) {
				continue
			}
			if _, ok := first[k]; !ok {
				first[k] = v
			}
			last[k] = v
		}
	}
	out := []DirGrowth{}
	for k, v := range last {
		if d := v - first[k]; d > 0 {
			out = append(out, DirGrowth{Path: k, Bytes: v, GrowthBytes: d})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GrowthBytes != out[j].GrowthBytes {
			return out[i].GrowthBytes > out[j].GrowthBytes
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > maxGrowth {
		out = out[:maxGrowth]
	}
	return out
}

// anomalies are growth over growth_gb_per_week within 7 days and new unknown directories over 2 GB (in the newest
// sample, in none before). Low disk is judged from the free space, not from samples.
func anomalies(env *core.Env, samples []sample) []Anomaly {
	var out []Anomaly
	if limit := env.Settings.GrowthBytesPerWeek(); limit > 0 {
		for _, g := range growthOf(samples, env.Now) {
			if g.GrowthBytes > limit {
				out = append(out, Anomaly{Kind: "growth", Path: g.Path, Bytes: g.GrowthBytes})
			}
		}
	}
	if n := len(samples); n >= 2 {
		old := map[string]bool{}
		for _, s := range samples[:n-1] {
			for k := range s.Sizes {
				old[k] = true
			}
		}
		var fresh []Anomaly
		for k, v := range samples[n-1].Sizes {
			if isPath(k) && !old[k] && v > newUnknownMin {
				fresh = append(fresh, Anomaly{Kind: "unknown-big", Path: k, Bytes: v})
			}
		}
		sort.Slice(fresh, func(i, j int) bool { return fresh[i].Path < fresh[j].Path })
		out = append(out, fresh...)
	}
	return out
}

// afterRun is the engine's AfterRun hook: at most once per 24 h it measures the unknown directories and the
// category totals of the run, stores them as a growth sample and tells the user, once per change, about growth
// and new big directories. A run that did not scan (paused, busy) or a dry run does not notify.
func afterRun(env *core.Env, st *state.Store, s *engine.Summary) {
	if st == nil || s == nil || s.Mode == "paused" || s.Mode == "busy" || s.Mode == "uninstalled" {
		return
	}
	if samples := readSamples(st); len(samples) > 0 && env.Now.Sub(samples[len(samples)-1].T) < sampleEvery {
		return
	}
	sizes := map[string]int64{}
	for _, d := range ScanUnknown(env, coveredPaths(env, s), ScanOptions{Budget: scanBudget, MinBytes: sampleMin, Max: sampleTop}) {
		sizes[d.Path] = d.Bytes
	}
	for _, cs := range [][]core.Candidate{s.Removed, s.WouldRemove} {
		for _, c := range cs {
			if c.Bytes > 0 {
				sizes[catPrefix+c.Category] += c.Bytes
			}
		}
	}
	st.AddGrowthSample(sizes)
	if s.Mode != "run" {
		return
	}
	// Deliberate exception to "facts go through env.Facts": the engine flushes its fact sink before it calls this
	// hook, so a fact given to env.Facts here would be dropped. The store is used directly, with the same
	// notify-once rule. Texts are fixed (no path, no number: details live in `report`), so the text alone never
	// changes; `changed` says when an anomaly of that kind is new since the previous sample.
	all := readSamples(st)
	if len(all) == 0 {
		return
	}
	now, before := anomalies(env, all), map[string]bool{}
	for _, a := range anomalies(env, all[:len(all)-1]) {
		before[a.Kind+"\x00"+a.Path] = true
	}
	changed := map[string]bool{}
	for _, a := range now {
		if !before[a.Kind+"\x00"+a.Path] {
			changed[a.Kind] = true
		}
	}
	for _, kind := range []string{"growth", "unknown-big"} {
		if !hasKind(now, kind) {
			continue
		}
		text := factText[kind]
		if st.Fact(kind, text, changed[kind]) && env.Settings.NotifyEnabled() && env.Platform != nil {
			if err := env.Platform.Notify("sapulidi", Clean(text)); err != nil {
				st.MarkUndelivered(kind)
			}
		}
	}
}

var factText = map[string]string{
	"growth":      "A directory grew a lot this week: see sapulidi report",
	"unknown-big": "A new large directory appeared: see sapulidi report",
}

func hasKind(as []Anomaly, kind string) bool {
	for _, a := range as {
		if a.Kind == kind {
			return true
		}
	}
	return false
}
