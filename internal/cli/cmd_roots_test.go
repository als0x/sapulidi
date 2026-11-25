package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/core"
)

func useRoots(t *testing.T, roots []core.Root, kept []core.Kept) {
	t.Helper()
	old := discoverRoots
	discoverRoots = func() ([]core.Root, []core.Kept, error) { return roots, kept, nil }
	t.Cleanup(func() { discoverRoots = old })
}

var rootsKept = []core.Kept{
	{Category: "roots", Path: "/work/evil\x1b[31m\nrepo\r‮gpj.txt", Why: "missing"},
	{Category: "roots", Path: "/work/mnt", Why: "device"},
	{Category: "roots", Path: "(codex) no ~/.codex/sessions, discovery skipped", Why: "no-codex"},
	{Category: "roots", Path: "", Why: "budget"},
}

func TestRootsShowsDropped(t *testing.T) {
	useRoots(t, []core.Root{{Path: "/work/app", Source: "claude"}, {Path: "/work/lib", Source: "config"}}, rootsKept)
	var out, errb bytes.Buffer
	if code := Main([]string{"roots"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	s := out.String()
	for _, want := range []string{"/work/app", "claude", "/work/lib", "config", "/work/mnt", "device", "missing"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	for _, bad := range []string{"\x1b", "\r", "‮"} {
		if strings.Contains(s, bad) {
			t.Fatalf("control character %q reached the output:\n%q", bad, s)
		}
	}
}

func TestRootsSaysWhyNextToEachToken(t *testing.T) {
	want := map[string]string{
		"deny": "never scanned as a project folder", "temp": "temporary folder",
		"not-git": "not a repository and holds none", "owner": "owned by another user",
		"device": "on another filesystem", "missing": "missing or not a folder",
		"cap": "over the limit on the number of roots", "nested": "inside another root",
		"outside": "its main repository is outside the folder",
	}
	var kept []core.Kept
	for why := range want {
		kept = append(kept, core.Kept{Category: "roots", Path: "/work/" + why, Why: why})
	}
	useRoots(t, nil, kept)
	code, out, e := runCLI(t, "roots")
	if code != 0 {
		t.Fatalf("%d %s", code, e)
	}
	for why, sentence := range want {
		line := fmt.Sprintf("  %-8s /work/%s (%s)\n", why, why, sentence)
		if !strings.Contains(out, line) {
			t.Errorf("missing %q in:\n%s", line, out)
		}
	}
}

func TestSafeTextMapsBidiAndControl(t *testing.T) {
	got := safeText("a‮b‏c⁦d\x1b\ne")
	if got != "a?b?c?d??e" {
		t.Fatalf("%q", got)
	}
	if safeText("plain /work/app é") != "plain /work/app é" {
		t.Fatal("ordinary text must pass unchanged")
	}
}

func TestRootsJSONSeparatesNotesFromDropped(t *testing.T) {
	useRoots(t, []core.Root{{Path: "/work/app", Source: "claude"}}, rootsKept)
	var out, errb bytes.Buffer
	if code := Main([]string{"--json", "roots"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	var doc struct {
		Roots   []struct{ Path, Source string }
		Dropped []struct{ Path, Reason string }
		Notes   []string
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(doc.Roots) != 1 || len(doc.Dropped) != 2 || len(doc.Notes) != 2 {
		t.Fatalf("%+v", doc)
	}
	for _, d := range doc.Dropped {
		if strings.ContainsAny(d.Path, "\x1b\r\n‮") {
			t.Fatalf("control character in dropped path %q", d.Path)
		}
		if d.Reason == "no-codex" || d.Reason == "budget" {
			t.Fatalf("%s is a note, not a dropped root", d.Reason)
		}
	}
	if bytes.Contains(out.Bytes(), []byte("null")) {
		t.Fatalf("null in %s", out.String())
	}
}
