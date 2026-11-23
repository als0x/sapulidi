package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/testutil"
)

func TestConfigSetUsesApply(t *testing.T) {
	home := testutil.TempHome(t)
	var out, errb bytes.Buffer
	if code := Main([]string{"config", "set", "category.deps.age", "45d"}, &out, &errb); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	applied := filepath.Join(home, ".config", "sapulidi", "applied.toml")
	b, err := os.ReadFile(applied)
	if err != nil || !strings.Contains(string(b), "45d") {
		t.Fatalf("applied.toml not written through config.Apply: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "sapulidi", "config.toml")); err == nil {
		t.Fatal("config.toml must never be rewritten")
	}
	out.Reset()
	if code := Main([]string{"--json", "config"}, &out, &errb); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	var doc struct {
		Config []struct{ Key, Value, Source string }
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("%v %s", err, out.String())
	}
	found := false
	for _, v := range doc.Config {
		if v.Key == "category.deps.age" && v.Value != "" && v.Source == config.SourceApplied {
			found = true
		}
	}
	if !found {
		t.Fatalf("effective config lacks the applied key: %s", out.String())
	}
}

func TestConfigSetRefusesNever(t *testing.T) {
	home := testutil.TempHome(t)
	var out, errb bytes.Buffer
	for _, args := range [][]string{
		{"config", "set", "category.models.enabled", "true"}, // never category
		{"config", "set", "category.deps.age", "30m"},        // below the minimum
		{"config", "set", "unknown.key", "1"},
	} {
		errb.Reset()
		if code := Main(args, &out, &errb); code != 1 || errb.Len() == 0 {
			t.Fatalf("%v: code %d %q", args, code, errb.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "sapulidi", "applied.toml")); err == nil {
		t.Fatal("a refused change must write nothing")
	}
	if code := Main([]string{"config", "set", "category.deps.age"}, &out, &errb); code != 2 {
		t.Fatalf("missing value is a usage error, got %d", code)
	}
}

func TestConfigPath(t *testing.T) {
	home := testutil.TempHome(t)
	var out, errb bytes.Buffer
	if code := Main([]string{"config", "path"}, &out, &errb); code != 0 {
		t.Fatalf("%d", code)
	}
	if strings.TrimSpace(out.String()) != filepath.Join(home, ".config", "sapulidi", "config.toml") {
		t.Fatalf("%q", out.String())
	}
}
