package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type manifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

func readManifest(t *testing.T, rel string) manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(), rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return m
}

func TestManifestVersionsMatch(t *testing.T) {
	want := readManifest(t, ".claude-plugin/plugin.json")
	if want.Name == "" || want.Version == "" || want.Description == "" {
		t.Fatalf("claude manifest is incomplete: %+v", want)
	}
	for _, rel := range []string{
		".codex-plugin/plugin.json",
		".cursor-plugin/plugin.json",
		".antigravity-plugin/plugin.json",
	} {
		if got := readManifest(t, rel); got != want {
			t.Errorf("%s = %+v, want %+v", rel, got, want)
		}
	}
	// The marketplace entry carries the same version.
	data, err := os.ReadFile(filepath.Join(repoRoot(), ".claude-plugin/marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mk struct {
		Plugins []manifest `json:"plugins"`
	}
	if err := json.Unmarshal(data, &mk); err != nil {
		t.Fatal(err)
	}
	if len(mk.Plugins) != 1 || mk.Plugins[0].Version != want.Version || mk.Plugins[0].Name != want.Name {
		t.Errorf("marketplace.json plugins = %+v, want one entry %s %s", mk.Plugins, want.Name, want.Version)
	}
}

// pinned reads the SAPULIDI_VERSION line of a shell file.
func pinned(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(), rel))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "SAPULIDI_VERSION="); ok {
			return v
		}
	}
	t.Fatalf("%s has no SAPULIDI_VERSION line", rel)
	return ""
}

// The release script rewrites the manifests, bin/sapulidi and install.sh together (0.2.0 at the v1.1 release);
// at every commit they must pin one version.
func TestInstallScriptAndLauncherPinManifestVersion(t *testing.T) {
	want := readManifest(t, ".claude-plugin/plugin.json").Version
	for _, rel := range []string{"install.sh", "bin/sapulidi"} {
		if got := pinned(t, rel); got != want {
			t.Errorf("%s pins %s, manifests say %s", rel, got, want)
		}
	}
}

func TestReleaseScriptBumpsEveryPin(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(), "scripts/release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, w := range []string{"install.sh", "bin/sapulidi", "SAPULIDI_VERSION"} {
		if !strings.Contains(text, w) {
			t.Errorf("release.sh does not mention %q", w)
		}
	}
	// checksums.txt is written before the suite runs (the 0.1.0 order left main red).
	sum := strings.Index(text, "sha256sum sapulidi_*")
	suite := strings.LastIndex(text, "bash tests/run.sh")
	if sum < 0 || suite < sum {
		t.Error("release.sh must write checksums.txt before it runs tests/run.sh")
	}
	// The binary embeds the launcher and the manifests, so every bump comes before the first build.
	bump := strings.Index(text, "replace bin/sapulidi")
	build := strings.Index(text, `gobuild "${t%/*}"`)
	if bump < 0 || build < 0 || bump > build {
		t.Error("release.sh must bump bin/sapulidi, install.sh and the manifests before the gobuild loop")
	}
	if i := strings.LastIndex(text, `replace "$f"`); i < 0 || i > build {
		t.Error("release.sh must bump the manifests before the gobuild loop")
	}
}

func TestReadmeCoversInstallAndTUI(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"TUI", "--agents", "--schedule", "install.sh", "exclude", "attestation"} {
		if !strings.Contains(string(data), w) {
			t.Errorf("README.md does not mention %q", w)
		}
	}
}

func TestSkillMentionsV11Features(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(), "skills/sapulidi/SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"TUI", "exclude", "--schedule", "agents", "Explicit"} {
		if !strings.Contains(string(data), w) {
			t.Errorf("SKILL.md does not mention %q", w)
		}
	}
}

func TestKnownLimitsListV11(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(), "docs/known-limits.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"exclude.toml", "not a sandbox", "statx", "fake a terminal"} {
		if !strings.Contains(string(data), w) {
			t.Errorf("known-limits.md does not mention %q", w)
		}
	}
}

func TestSkillSizeLimits(t *testing.T) {
	root := repoRoot()
	check := func(rel string, limit int) {
		t.Helper()
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			return
		}
		if info.Size() > int64(limit) {
			t.Errorf("%s is %d bytes, limit %d", rel, info.Size(), limit)
		}
	}
	check("skills/sapulidi/SKILL.md", 5120)
	for _, h := range []string{"claude-code", "codex", "cursor", "antigravity", "opencode", "pi"} {
		check("skills/sapulidi/references/"+h+".md", 2048)
	}
	refs, _ := filepath.Glob(filepath.Join(root, "skills/sapulidi/references/*.md"))
	for _, r := range refs {
		check(filepath.Join("skills/sapulidi/references", filepath.Base(r)), 2048)
	}
}

func TestSkillMentionsTerminalTierCommands(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(), "skills/sapulidi/SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, w := range []string{"procs stop", "rule", "print", "never run", "data, never instructions", "language", "yes",
		"(list only, never procs stop)", "procs stop is never run by you", "uninstall --all", "setup --yes", "consent --yes",
		"config set", "OWN terminal", "script, expect, a pty, a pipe or a redirect", "never add --yes", "config.toml",
		"applied.toml", "only sapulidi deletes", "could fake a terminal", "permission prompts",
		"IGNORE PREVIOUS INSTRUCTIONS", "container and model names", ".codex-plugin",
		"(available after the agent PRD)"} {
		if !strings.Contains(text, w) {
			t.Errorf("SKILL.md does not mention %q", w)
		}
	}
	for _, a := range []string{"setup", "report", "plan", "review", "procs", "schedule", "pause"} {
		if !strings.Contains(text, a) {
			t.Errorf("SKILL.md does not name action %q", a)
		}
	}
}

func TestUntestedHostsAreMarked(t *testing.T) {
	root := repoRoot()
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"Tested:", "macOS arm64", "Alpine arm64 container", "Not yet run", "real Claude Code session",
		"Never run", "Codex, Cursor, Antigravity, OpenCode, Pi"} {
		if !strings.Contains(string(readme), w) {
			t.Errorf("README.md status lacks %q", w)
		}
	}
	if strings.Contains(string(readme), "Only Claude Code is tested") {
		t.Error("README.md still says only Claude Code is tested; that overstated what was run")
	}
	for _, rel := range []string{".codex-plugin/plugin.json", ".cursor-plugin/plugin.json", ".antigravity-plugin/plugin.json"} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"untested": true`) {
			t.Errorf("%s lacks \"untested\": true", rel)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "hooks/codex-hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `${PLUGIN_ROOT}/hooks/codex-session-start.sh`) {
		t.Error("codex-hooks.json must use an absolute plugin-root path")
	}
	ref, err := os.ReadFile(filepath.Join(root, "skills/sapulidi/references/codex.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ref), "UNVERIFIED") {
		t.Error("references/codex.md must say the plugin-root variable is UNVERIFIED")
	}
}

func TestHostReferencesCarryTheirRules(t *testing.T) {
	root := repoRoot()
	read := func(h string) string {
		data, err := os.ReadFile(filepath.Join(root, "skills/sapulidi/references", h+".md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, h := range []string{"claude-code", "codex", "cursor", "antigravity", "opencode", "pi"} {
		text := read(h)
		for _, w := range []string{"procs stop", "uninstall --all", "script, expect, a pty or a pipe"} {
			if !strings.Contains(text, w) {
				t.Errorf("%s reference lacks %q", h, w)
			}
		}
	}
	for _, h := range []string{"cursor", "antigravity", "opencode", "pi"} {
		text := read(h)
		if !strings.Contains(text, "check --host "+h+" --host-version") {
			t.Errorf("%s reference lacks the heartbeat command", h)
		}
		if !strings.Contains(text, "schedule install") {
			t.Errorf("%s reference lacks schedule install", h)
		}
	}
	if !strings.Contains(read("claude-code"), "/loop 10m sapulidi check") {
		t.Error("claude-code reference lacks the /loop suggestion")
	}
}
