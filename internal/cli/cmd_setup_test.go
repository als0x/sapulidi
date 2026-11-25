package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
)

func setupFixture(t *testing.T) {
	t.Helper()
	labelFixture(t)
	var kept []core.Kept
	for _, k := range [][2]string{
		{"/srv/me", "deny"}, {"/tmp/work", "temp"}, {"/work/notes", "not-git"}, {"/work/shared", "owner"},
		{"/mnt/disk", "device"}, {"/work/gone", "missing"}, {"/work/many", "cap"}, {"", "budget"},
		{"(codex) no ~/.codex/sessions, discovery skipped", "no-codex"}, {"/work/evil\x1b[2Krepo", "missing"},
	} {
		kept = append(kept, core.Kept{Category: "roots", Path: k[0], Why: k[1]})
	}
	useRoots(t, nil, kept)
}

func TestSetupReport(t *testing.T) {
	setupFixture(t)
	code, out, e := runCLI(t, "setup")
	if code != 0 {
		t.Fatalf("%d %q", code, e)
	}
	golden(t, "setup.golden", out)
	if strings.Contains(out, "roots =") {
		t.Fatal("setup must not tell anyone to write roots")
	}
}

func TestSetupWithoutRootsDoesNotSayToWriteThem(t *testing.T) {
	setupFixture(t)
	env, _ := BuildEnv()
	env.Roots = nil
	code, out, e := runCLI(t, "setup")
	want := "Roots: none found · 8 skipped (run 'sapulidi roots' for the full list)\n"
	if code != 0 || !strings.Contains(out, want) {
		t.Fatalf("%d %q %q", code, out, e)
	}
	useRoots(t, nil, nil)
	code, out, e = runCLI(t, "setup")
	if code != 0 || !strings.Contains(out, "Roots: none found\n") || strings.Contains(out, "skipped") {
		t.Fatalf("no skipped: %d %q %q", code, out, e)
	}
}

func TestSetupYesRecordsConsent(t *testing.T) {
	safe := []string{"agent/claude-scratch@1", "temp/go-build@1"}
	both := []string{"agent/claude-scratch@1", "rust/target@2", "temp/go-build@1"}
	for name, tc := range map[string]struct {
		args     []string
		terminal bool
		typed    string
		want     []string
	}{
		"no terminal":      {args: []string{"setup", "--yes"}, want: safe},
		"flag":             {args: []string{"setup", "--yes", "--recommended"}, terminal: true, typed: "allow\n", want: both},
		"terminal y":       {args: []string{"setup", "--yes"}, terminal: true, typed: "y\n", want: both},
		"terminal n":       {args: []string{"setup", "--yes"}, terminal: true, typed: "n\n", want: safe},
		"terminal enter":   {args: []string{"setup", "--yes"}, terminal: true, typed: "\n", want: safe},
		"json never asks":  {args: []string{"--json", "setup", "--yes"}, terminal: true, typed: "y\n", want: safe},
		"json with a flag": {args: []string{"--json", "setup", "--yes", "--recommended"}, terminal: true, typed: "allow\n", want: both},
	} {
		t.Run(name, func(t *testing.T) {
			setupFixture(t)
			withTerminal(t, tc.terminal, tc.typed)
			code, out, e := runCLI(t, tc.args...)
			if code != 0 {
				t.Fatalf("%d %q %q", code, out, e)
			}
			st, err := OpenStore()
			if err != nil {
				t.Fatal(err)
			}
			if got := consentedKeys(t, st); !slices.Equal(got, tc.want) {
				t.Fatalf("consented %v, want %v\n%s", got, tc.want, out)
			}
			if tc.args[0] == "--json" {
				return
			}
			want := "consent recorded for 2 rules"
			if len(tc.want) == 3 {
				want = "consent recorded for 3 rules"
			}
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q in %q", want, out)
			}
			if hint := "sapulidi consent --yes --recommended"; strings.Contains(out, hint) != (len(tc.want) == 2) {
				t.Fatalf("hint %q wrong in %q", hint, out)
			}
		})
	}
}

func TestSetupAsksTheQuestion(t *testing.T) {
	setupFixture(t)
	tty := recordingTTY(t, "n\n")
	if code, _, e := runCLI(t, "setup", "--yes"); code != 0 {
		t.Fatalf("%d %q", code, e)
	}
	want := "Also let the schedule remove recommended rows (1 item(s), 4.0 KiB)? [y/N] "
	if got := tty.String(); got != want {
		t.Fatalf("question %q, want %q", got, want)
	}
	if got := setupRecommendedPrompt(nil); got != recommendedQuestion {
		t.Fatalf("empty fallback %q, want %q", got, recommendedQuestion)
	}
}

func TestSetupYesRecommendedNeedsTerminal(t *testing.T) {
	setupFixture(t)
	st, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	const retry = "run this in your own terminal: sapulidi setup --yes --recommended"
	code, _, e := runCLI(t, "setup", "--yes", "--recommended")
	if code != 1 || !strings.Contains(e, retry) {
		t.Fatalf("no tty: %d %q", code, e)
	}
	code, out, _ := runCLI(t, "--json", "setup", "--yes", "--recommended")
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 1 || doc["error"] != retry {
		t.Fatalf("json: %d %q %v", code, out, err)
	}
	withTerminal(t, true, "y\n")
	if code, _, e := runCLI(t, "setup", "--yes", "--recommended"); code != 1 || !strings.Contains(e, "not confirmed") {
		t.Fatalf("mismatch: %d %q", code, e)
	}
	if got, err := st.Consent(); err != nil || got != nil {
		t.Fatalf("a refused setup recorded %v %v", got, err)
	}
}

func TestSetupRecommendedNeedsYes(t *testing.T) {
	setupFixture(t)
	if code, _, _ := runCLI(t, "setup", "--recommended"); code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
}

func TestSetupCompactWithManyRootsAndSkipped(t *testing.T) {
	setupFixture(t)
	env, _ := BuildEnv()
	env.Roots = make([]core.Root, 0, 50)
	sources := []string{"claude", "claude+children", "codex\x1b[2K"}
	for i := range 50 {
		env.Roots = append(env.Roots, core.Root{
			Path:   fmt.Sprintf("/work/repo-%02d", i),
			Source: sources[i%len(sources)],
		})
	}
	kept := make([]core.Kept, 0, 50)
	for i := range 50 {
		kept = append(kept, core.Kept{
			Category: "roots",
			Path:     fmt.Sprintf("/work/skipped-%02d", i),
			Why:      "not-git",
		})
	}
	useRoots(t, env.Roots, kept)
	code, out, e := runCLI(t, "setup")
	if code != 0 {
		t.Fatalf("%d %q", code, e)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) >= 25 {
		t.Fatalf("expected compact output (< 25 lines), got %d lines:\n%s", len(lines), out)
	}
	wantRoot := "Roots: 50 repo(s) (claude, claude+children, codex?[2K) · 50 skipped (run 'sapulidi roots' for the full list)"
	if !strings.Contains(out, wantRoot) {
		t.Fatalf("missing %q in:\n%s", wantRoot, out)
	}
	if strings.Contains(out, "\x1b") {
		t.Fatalf("unsanitized escape sequence in:\n%s", out)
	}
}

func TestSetupOmitsCategoriesWhenAllEnabled(t *testing.T) {
	setupFixture(t)
	env, _ := BuildEnv()
	env.Settings.(*testutil.FakeSettings).Categories["agent-transcripts"] = true
	code, out, e := runCLI(t, "setup")
	if code != 0 {
		t.Fatalf("%d %q", code, e)
	}
	if strings.Contains(out, "Categories") {
		t.Fatalf("expected Categories line to be omitted when all non-never categories are enabled:\n%s", out)
	}
}
