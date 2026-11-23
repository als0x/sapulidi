package sapulidi

import (
	"encoding/json"
	"io/fs"
	"testing"

	"github.com/als0x/sapulidi/internal/version"
)

func TestEmbeddedPluginManifest(t *testing.T) {
	b, err := fs.ReadFile(Plugin, ".claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "sapulidi" || m.Version == "" {
		t.Fatalf("manifest %+v", m)
	}
	// A plain `go test` build carries the dev placeholder (release builds set it with ldflags), so the
	// equality is checked only for a stamped version.
	if version.Version != "0.0.0-dev" && m.Version != version.Version {
		t.Fatalf("plugin version %q, binary version %q", m.Version, version.Version)
	}
}

func TestEmbeddedPluginHasHostFiles(t *testing.T) {
	for _, p := range []string{
		".claude-plugin/marketplace.json", ".codex-plugin/plugin.json", ".cursor-plugin/plugin.json",
		".antigravity-plugin/plugin.json", "skills/sapulidi/SKILL.md", "skills/sapulidi/references/codex.md",
		"hooks/hooks.json", "bin/sapulidi", "README.md",
	} {
		if _, err := fs.Stat(Plugin, p); err != nil {
			t.Errorf("%s not embedded: %v", p, err)
		}
	}
}
