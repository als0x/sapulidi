// Package models measures local model stores (Ollama, LM Studio, Hugging Face) for `report`. The category is
// never: its candidates are report-only and nothing here can remove anything (spec 3.1, rules in
// packs/models.toml).
package models

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/report/sizing"
)

const (
	locOllama      = "ollama"
	locLMStudio    = "lmstudio"
	locHuggingFace = "huggingface"
	callTimeout    = 15 * time.Second
)

func init() {
	for _, l := range []string{locOllama, locLMStudio, locHuggingFace} {
		core.RegisterLocation(l)
	}
	core.Register(&cat{})
}

// Usage is one model, or one store when its tool cannot list models.
type Usage struct {
	Tool      string `json:"tool"`
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Bytes     int64  `json:"bytes"`
	SizeKnown bool   `json:"size_known"`          // false when the size is partial (unreadable entry or budget)
	LastUsed  string `json:"last_used,omitempty"` // RFC 3339, only when the filesystem keeps access times
}

// Store is a directory that holds models, with the location id of its rule.
type Store struct{ Location, Path string }

// Stores returns the model directories of this machine that exist.
func Stores(env *core.Env) []Store {
	hub := filepath.Join(env.Home, ".cache", "huggingface", "hub")
	if hf := os.Getenv("HF_HOME"); filepath.IsAbs(hf) {
		hub = filepath.Join(hf, "hub")
	}
	all := []Store{
		{locOllama, filepath.Join(env.Home, ".ollama", "models")},
		{locLMStudio, filepath.Join(env.Home, ".lmstudio", "models")},
		{locLMStudio, filepath.Join(env.Home, ".cache", "lm-studio")},
		{locHuggingFace, hub},
	}
	var out []Store
	for _, s := range all {
		if fi, err := os.Lstat(s.Path); err == nil && fi.IsDir() {
			out = append(out, s)
		}
	}
	return out
}

// Usages lists the models: one entry per Ollama model (or per store when `ollama list` is unavailable), one per
// LM Studio folder and one per Hugging Face repository folder.
func Usages(env *core.Env) []Usage {
	ctx := sizing.Parent(env.Ctx)
	out := []Usage{}
	for _, s := range Stores(env) {
		switch s.Location {
		case locOllama:
			if list := ollamaList(env, s.Path); len(list) > 0 {
				out = append(out, list...)
				continue
			}
			n, known := sizing.Dir(ctx, s.Path)
			out = append(out, Usage{Tool: "ollama", Name: "models", Path: s.Path, Bytes: n, SizeKnown: known})
		case locLMStudio:
			n, known := sizing.Dir(ctx, s.Path)
			out = append(out, Usage{Tool: "lmstudio", Name: filepath.Base(s.Path), Path: s.Path, Bytes: n, SizeKnown: known})
		case locHuggingFace:
			out = append(out, hubUsages(ctx, s.Path)...)
		}
	}
	return out
}

func ollamaList(env *core.Env, dir string) []Usage {
	if env.Run == nil {
		return nil
	}
	if _, err := env.Run.LookPath("ollama"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(sizing.Parent(env.Ctx), callTimeout)
	defer cancel()
	b, err := env.Run.Run(ctx, env.Home, "ollama", "list")
	if err != nil {
		return nil
	}
	var out []Usage
	for i, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		// NAME ID SIZE UNIT MODIFIED...
		if i == 0 || len(f) < 4 {
			continue
		}
		n, ok := parseSize(f[2], f[3])
		if !ok {
			continue
		}
		out = append(out, Usage{Tool: "ollama", Name: f[0], Path: dir, Bytes: n, SizeKnown: true})
	}
	return out
}

func parseSize(num, unit string) (int64, bool) {
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	mult := map[string]float64{"B": 1, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12}
	m, ok := mult[strings.ToUpper(unit)]
	if !ok {
		return 0, false
	}
	return int64(v*m + 0.5), true
}

// hubUsages makes one entry per repository folder (models--org--name) of a Hugging Face hub cache.
func hubUsages(ctx context.Context, hub string) []Usage {
	entries, err := os.ReadDir(hub)
	if err != nil {
		return nil
	}
	var out []Usage
	for _, e := range entries {
		if !e.IsDir() || ctx.Err() != nil {
			continue
		}
		p := filepath.Join(hub, e.Name())
		n, known := sizing.Dir(ctx, p)
		u := Usage{Tool: "huggingface", Name: hubName(e.Name()), Path: p, Bytes: n, SizeKnown: known}
		if t := lastAccess(ctx, p); !t.IsZero() {
			u.LastUsed = t.UTC().Format(time.RFC3339)
		}
		out = append(out, u)
	}
	return out
}

func hubName(dir string) string {
	name := strings.ReplaceAll(dir, "--", "/")
	return strings.TrimPrefix(name, "models/")
}

// lastAccess is the newest access time of a regular file under dir; zero when none is known.
func lastAccess(ctx context.Context, dir string) time.Time {
	var newest time.Time
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			if t, ok := atimeOf(fi); ok && t.After(newest) {
				newest = t
			}
		}
		return nil
	})
	return newest
}

type cat struct{}

func (*cat) Name() string        { return "models" }
func (*cat) Bucket() core.Bucket { return core.Never }

// Scan reports each model store as a report-only candidate; the engine never scans a never category.
func (*cat) Scan(env *core.Env) ([]core.Candidate, []core.Kept, error) {
	rules := map[string]core.Rule{}
	for _, r := range env.Rules {
		if r.Category == "models" && r.Kind == core.KindPath {
			rules[r.Location] = r
		}
	}
	var out []core.Candidate
	for _, s := range Stores(env) {
		r, ok := rules[s.Location]
		if !ok {
			continue
		}
		n, known := sizing.Dir(sizing.Parent(env.Ctx), s.Path)
		if !known {
			n = -1
		}
		out = append(out, core.Candidate{Category: "models", RuleKey: r.Key(), Path: s.Path, Base: s.Path,
			Removal: core.ReportOnly, Reason: "model store, shown by report and never removed", Bytes: n})
	}
	return out, nil, nil
}

func (*cat) Proof(*core.Env, core.Candidate) error {
	return core.Keep("deny", "models are never removed")
}
