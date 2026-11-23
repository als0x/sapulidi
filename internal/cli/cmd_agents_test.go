package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/testutil"
)

func agentsRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(args, &out, &errb)
	return code, out.String(), errb.String()
}

func agentsEnv(t *testing.T) string {
	t.Helper()
	home := testutil.TempHome(t)
	t.Setenv("PATH", t.TempDir()+":/usr/bin:/bin")
	return home
}

func TestAgentsListJSONShape(t *testing.T) {
	home := agentsEnv(t)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, _ := agentsRun(t, "agents", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var doc struct {
		Hosts []struct {
			Host struct {
				Name    string `json:"name"`
				Command string `json:"command"`
				Tested  bool   `json:"tested"`
			} `json:"host"`
			Present   bool `json:"present"`
			Installed bool `json:"installed"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Hosts) != 6 {
		t.Fatalf("bad document %q: %v", out, err)
	}
	for _, h := range doc.Hosts {
		if h.Present != (h.Host.Name == "codex") || h.Installed || h.Host.Tested != (h.Host.Name == "claude") {
			t.Fatalf("row %+v", h)
		}
	}
}

func TestAgentsListHumanMarksUntested(t *testing.T) {
	agentsEnv(t)
	code, out, _ := agentsRun(t, "agents")
	if code != 0 || !strings.Contains(out, "codex") || !strings.Contains(out, "untested") {
		t.Fatalf("exit %d: %s", code, out)
	}
}

func TestAgentsUsageErrors(t *testing.T) {
	agentsEnv(t)
	for _, args := range [][]string{{"agents", "bogus"}, {"agents", "install"}, {"agents", "install", "nope"}, {"agents", "remove", "a", "b"}} {
		if code, _, _ := agentsRun(t, args...); code != 2 {
			t.Fatalf("%v exit %d", args, code)
		}
	}
}

func TestAgentsInstallAndRemoveCopiedSkill(t *testing.T) {
	home := agentsEnv(t)
	skills := filepath.Join(home, ".cursor", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, _ := agentsRun(t, "agents", "install", "cursor")
	if code != 0 || !strings.Contains(out, "untested") {
		t.Fatalf("exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(skills, "sapulidi", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	code, out, _ = agentsRun(t, "agents", "--json")
	if code != 0 || !strings.Contains(out, `"installed":true`) {
		t.Fatalf("not listed as installed: %s", out)
	}
	if code, out, _ := agentsRun(t, "agents", "remove", "cursor", "--json"); code != 0 || !strings.Contains(out, `"status":"removed"`) {
		t.Fatalf("exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(skills, "sapulidi")); err == nil {
		t.Fatal("skill folder left behind")
	}
}

func TestAgentsInstallSkippedExitsZero(t *testing.T) {
	home := agentsEnv(t)
	code, out, _ := agentsRun(t, "agents", "install", "all")
	if code != 0 || !strings.Contains(out, "skipped") {
		t.Fatalf("exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor")); err == nil {
		t.Fatal("created a tool folder")
	}
}
