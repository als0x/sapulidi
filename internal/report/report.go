// Package report answers "what uses my disk": every category, local models, the biggest directories no rule knows
// and how they grow (spec 6 and 8). It only reads; the AfterRun hook it installs writes growth samples to the
// state store, nothing else. Every path, name and detail in the Report is passed through Clean, because file
// names are untrusted text.
package report

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/als0x/sapulidi/internal/categories/models"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/report/sizing"
	"github.com/als0x/sapulidi/internal/state"
)

type CategoryUsage struct {
	Category       string `json:"category"`
	Bucket         string `json:"bucket"`
	Enabled        bool   `json:"enabled"`
	Candidates     int    `json:"candidates"`
	CandidateBytes int64  `json:"candidate_bytes"`
	Kept           int    `json:"kept"`
	KeptBytes      int64  `json:"kept_bytes"`
	KeptSizeKnown  bool   `json:"kept_size_known"`
}

type DirUsage struct {
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
	SizeKnown bool   `json:"size_known"`
}

type DirGrowth struct {
	Path        string `json:"path"`
	Bytes       int64  `json:"bytes"`
	GrowthBytes int64  `json:"growth_bytes"`
}

type ModelUsage = models.Usage

// Anomaly Kind is growth, low-disk or unknown-big.
type Anomaly struct {
	Kind  string `json:"kind"`
	Path  string `json:"path,omitempty"`
	Bytes int64  `json:"bytes"`
}

// Report is the whole answer. Personal holds the total size of Downloads, Documents and Desktop, which are
// never scanned inside. Every list is [] when empty, never null.
type Report struct {
	Free       uint64          `json:"free_bytes"`
	Total      uint64          `json:"total_bytes"`
	Categories []CategoryUsage `json:"categories"`
	Unknown    []DirUsage      `json:"unknown"`
	Personal   []DirUsage      `json:"personal"`
	Growth     []DirGrowth     `json:"growth"`
	Models     []ModelUsage    `json:"models"`
	Anomalies  []Anomaly       `json:"anomalies"`
}

const (
	scanBudget   = 30 * time.Second
	maxUnknown   = 20
	maxGrowth    = 20
	personalSize = 30 * time.Second
)

// Build plans a dry run to size what would go, scans for unknown big directories (30 s budget), measures the
// model stores and reads the growth samples of st (nil: no growth). It removes nothing.
func Build(env *core.Env, cats []core.Category, st *state.Store) (Report, error) {
	sum := engine.Plan(env, cats, engine.Options{})
	r := Report{Free: sum.FreeBefore, Total: totalBytes(env.Home), Categories: []CategoryUsage{},
		Personal: []DirUsage{}, Growth: []DirGrowth{}, Anomalies: []Anomaly{}}

	ctx, cancel := context.WithTimeout(sizing.Parent(env.Ctx), personalSize)
	defer cancel()
	sorted := append([]core.Category(nil), cats...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name() < sorted[j].Name() })
	index := map[string]int{}
	for _, c := range sorted {
		index[c.Name()] = len(r.Categories)
		r.Categories = append(r.Categories, CategoryUsage{Category: c.Name(), Bucket: string(c.Bucket()),
			Enabled: env.Settings.CategoryEnabled(c.Name()), KeptSizeKnown: true})
	}
	for _, c := range sum.WouldRemove {
		if i, ok := index[c.Category]; ok {
			r.Categories[i].Candidates++
			if c.Bytes > 0 {
				r.Categories[i].CandidateBytes += c.Bytes
			}
		}
	}
	for _, k := range sum.Kept {
		i, ok := index[k.Category]
		if !ok {
			continue
		}
		r.Categories[i].Kept++
		if k.Path != "" {
			n, known := sizing.Dir(ctx, k.Path)
			r.Categories[i].KeptBytes += n
			r.Categories[i].KeptSizeKnown = r.Categories[i].KeptSizeKnown && known
		}
	}

	r.Unknown = ScanUnknown(env, coveredPaths(env, &sum), ScanOptions{Budget: scanBudget, MinBytes: unknownMin, Max: maxUnknown})
	for _, d := range personalDirs(env) {
		if _, err := os.Lstat(d); err == nil {
			n, known := sizing.Dir(ctx, d)
			r.Personal = append(r.Personal, DirUsage{Path: d, Bytes: n, SizeKnown: known})
		}
	}
	r.Models = models.Usages(env)

	if st != nil {
		samples := readSamples(st)
		r.Growth = growthOf(samples, env.Now)
		r.Anomalies = append(r.Anomalies, anomalies(env, samples)...)
	}
	if limit := env.Settings.LowDiskBytes(); limit > 0 && r.Free > 0 && r.Free < limit {
		r.Anomalies = append(r.Anomalies, Anomaly{Kind: "low-disk", Bytes: int64(r.Free)})
	}
	r.clean()
	return r, nil
}

// clean passes every text of the report through Clean.
func (r *Report) clean() {
	for i := range r.Categories {
		r.Categories[i].Category = Clean(r.Categories[i].Category)
	}
	for i := range r.Unknown {
		r.Unknown[i].Path = Clean(r.Unknown[i].Path)
	}
	for i := range r.Personal {
		r.Personal[i].Path = Clean(r.Personal[i].Path)
	}
	for i := range r.Growth {
		r.Growth[i].Path = Clean(r.Growth[i].Path)
	}
	for i := range r.Models {
		m := &r.Models[i]
		m.Tool, m.Name, m.Path, m.LastUsed = Clean(m.Tool), Clean(m.Name), Clean(m.Path), Clean(m.LastUsed)
	}
	for i := range r.Anomalies {
		r.Anomalies[i].Path = Clean(r.Anomalies[i].Path)
	}
	if r.Unknown == nil {
		r.Unknown = []DirUsage{}
	}
	if r.Models == nil {
		r.Models = []ModelUsage{}
	}
}

// coveredPaths are the places a rule or a known location already accounts for: every candidate and kept path of
// the summary and the model stores.
func coveredPaths(env *core.Env, s *engine.Summary) []string {
	var out []string
	for _, cs := range [][]core.Candidate{s.Removed, s.WouldRemove} {
		for _, c := range cs {
			if c.Path != "" {
				out = append(out, c.Path)
			}
		}
	}
	for _, k := range s.Kept {
		if k.Path != "" {
			out = append(out, k.Path)
		}
	}
	for _, m := range models.Stores(env) {
		out = append(out, m.Path)
	}
	return out
}

func personalDirs(env *core.Env) []string {
	var out []string
	for _, n := range []string{"Desktop", "Documents", "Downloads"} {
		out = append(out, filepath.Join(env.Home, n))
	}
	return out
}

func totalBytes(path string) uint64 {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0
	}
	return uint64(st.Blocks) * uint64(st.Bsize)
}
