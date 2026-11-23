package sapulidi

import (
	"encoding/json"
	"io/fs"
	"os"
	"testing"
)

func pluginVersion(t *testing.T, b []byte) string {
	t.Helper()
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
	return m.Version
}

// The embedded manifest is the one in the repository. (internal/version holds a dev placeholder in a plain
// build and is stamped only by the release build, so it cannot be compared here.)
func TestEmbeddedPluginManifestMatchesRepository(t *testing.T) {
	emb, err := fs.ReadFile(Plugin, ".claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(".claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	if a, b := pluginVersion(t, emb), pluginVersion(t, disk); a != b {
		t.Fatalf("embedded version %q, repository version %q", a, b)
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
