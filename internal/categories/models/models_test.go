package models

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	packsload "github.com/als0x/sapulidi/internal/packs"
	"github.com/als0x/sapulidi/internal/testutil"
	"github.com/als0x/sapulidi/packs"
)

func loadRules(t *testing.T) []core.Rule {
	t.Helper()
	data, err := packs.FS.ReadFile("models.toml")
	if err != nil {
		t.Fatal(err)
	}
	rules, errs := packsload.Load(fstest.MapFS{"models.toml": {Data: data}}, "")
	if len(errs) > 0 {
		t.Fatalf("pack errors: %v", errs)
	}
	return rules
}

func newEnv(t *testing.T, run *testutil.FakeRunner) *core.Env {
	t.Helper()
	home := testutil.TempHome(t)
	t.Setenv("HF_HOME", "")
	if run == nil {
		run = &testutil.FakeRunner{}
	}
	return &core.Env{Home: home, Now: time.Now(), Rules: loadRules(t), Platform: &testutil.FakePlatform{},
		Run: run, Settings: &testutil.FakeSettings{}}
}

func write(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

const ollamaOut = `NAME               ID              SIZE      MODIFIED
llama3:8b          365c0bd3c000    4.7 GB    2 weeks ago
nomic-embed-text   0a109f422b47    274 MB    3 months ago
`

func TestModelsOllamaList(t *testing.T) {
	run := &testutil.FakeRunner{Paths: map[string]string{"ollama": "/bin/ollama"}}
	run.On([]string{"ollama", "list"}, ollamaOut, nil)
	env := newEnv(t, run)
	write(t, filepath.Join(env.Home, ".ollama", "models", "blobs", "x"), 10)

	var got []Usage
	for _, u := range Usages(env) {
		if u.Tool == "ollama" {
			got = append(got, u)
		}
	}
	if len(got) != 2 {
		t.Fatalf("want 2 ollama models, got %+v", got)
	}
	if got[0].Name != "llama3:8b" || got[0].Bytes != 4_700_000_000 {
		t.Errorf("first model: %+v", got[0])
	}
	if got[1].Name != "nomic-embed-text" || got[1].Bytes != 274_000_000 {
		t.Errorf("second model: %+v", got[1])
	}
}

func TestModelsOllamaFallsBackToDirectory(t *testing.T) {
	env := newEnv(t, nil) // ollama not on PATH
	write(t, filepath.Join(env.Home, ".ollama", "models", "blobs", "x"), 8192)
	var got []Usage
	for _, u := range Usages(env) {
		if u.Tool == "ollama" {
			got = append(got, u)
		}
	}
	if len(got) != 1 || got[0].Bytes <= 0 || got[0].Path != filepath.Join(env.Home, ".ollama", "models") {
		t.Fatalf("fallback: %+v", got)
	}
}

func TestModelsLMStudioAndHuggingFace(t *testing.T) {
	env := newEnv(t, nil)
	write(t, filepath.Join(env.Home, ".lmstudio", "models", "a", "m.gguf"), 4096)
	hub := filepath.Join(env.Home, ".cache", "huggingface", "hub")
	write(t, filepath.Join(hub, "models--org--name", "blobs", "b"), 4096)
	write(t, filepath.Join(hub, "datasets--org--data", "blobs", "b"), 4096)
	byName := map[string]Usage{}
	for _, u := range Usages(env) {
		byName[u.Tool+":"+u.Name] = u
	}
	for _, k := range []string{"lmstudio:models", "huggingface:org/name", "huggingface:datasets/org/data"} {
		if _, ok := byName[k]; !ok {
			t.Errorf("missing %s in %v", k, byName)
		}
	}
	// HF_HOME wins over the default folder.
	other := filepath.Join(env.Home, "hf")
	write(t, filepath.Join(other, "hub", "models--x--y", "blobs", "b"), 4096)
	t.Setenv("HF_HOME", other)
	n := 0
	for _, u := range Usages(env) {
		if u.Tool == "huggingface" {
			n++
			if u.Name != "x/y" {
				t.Errorf("unexpected entry %+v", u)
			}
		}
	}
	if n != 1 {
		t.Errorf("want one entry under HF_HOME, got %d", n)
	}
}

func TestModelsNeverCandidates(t *testing.T) {
	env := newEnv(t, nil)
	write(t, filepath.Join(env.Home, ".ollama", "models", "blobs", "x"), 4096)
	write(t, filepath.Join(env.Home, ".lmstudio", "models", "m.gguf"), 4096)
	c := &cat{}
	if c.Bucket() != core.Never {
		t.Fatal("models must be a never category")
	}
	cands, _, err := c.Scan(env)
	if err != nil || len(cands) != 2 {
		t.Fatalf("scan: %v %+v", err, cands)
	}
	for _, cd := range cands {
		if cd.Removal != core.ReportOnly {
			t.Errorf("%s is removable: %s", cd.Path, cd.Removal)
		}
		if err := c.Proof(env, cd); err == nil {
			t.Errorf("proof allowed %s", cd.Path)
		}
	}
}
