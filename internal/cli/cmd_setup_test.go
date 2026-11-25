package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/core"
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
	if code != 0 || !strings.Contains(out, "Roots:\n  none found\n") {
		t.Fatalf("%d %q %q", code, out, e)
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
		"flag":             {args: []string{"setup", "--yes", "--recommended"}, want: both},
		"terminal y":       {args: []string{"setup", "--yes"}, terminal: true, typed: "y\n", want: both},
		"terminal n":       {args: []string{"setup", "--yes"}, terminal: true, typed: "n\n", want: safe},
		"terminal enter":   {args: []string{"setup", "--yes"}, terminal: true, typed: "\n", want: safe},
		"json never asks":  {args: []string{"--json", "setup", "--yes"}, terminal: true, typed: "y\n", want: safe},
		"json with a flag": {args: []string{"--json", "setup", "--yes", "--recommended"}, want: both},
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
	if got := tty.String(); got != "Also let the schedule remove recommended rows? [y/N] " {
		t.Fatalf("question %q", got)
	}
}

func TestSetupRecommendedNeedsYes(t *testing.T) {
	setupFixture(t)
	if code, _, _ := runCLI(t, "setup", "--recommended"); code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
}
