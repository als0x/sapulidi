package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/install"
)

// recordCommands replaces agents and schedule with recording fakes; the returned slice
// holds one "name arg arg" entry per call. codes maps a command name to its exit code.
func recordCommands(t *testing.T, codes map[string]int) *[]string {
	t.Helper()
	var calls []string
	saved := map[string]Command{}
	for _, n := range []string{"agents", "schedule"} {
		saved[n] = commands[n]
		name := n
		Register(Command{Name: name, Run: func(_ *App, args []string) int {
			calls = append(calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
			return codes[name]
		}})
	}
	t.Cleanup(func() {
		for n, c := range saved {
			commands[n] = c
		}
	})
	return &calls
}

func runFront(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestFrontMappings(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"--agents"}, []string{"agents"}},
		{[]string{"--agents", "claude"}, []string{"agents install claude"}},
		{[]string{"--agents", "all"}, []string{"agents install all"}},
		{[]string{"--agents", "pi", "--remove"}, []string{"agents remove pi"}},
		{[]string{"--agents", "all", "--remove"}, []string{"agents remove all"}},
		{[]string{"--schedule", "30m"}, []string{"schedule install --every 30m"}},
		{[]string{"--schedule", "off"}, []string{"schedule remove"}},
		{[]string{"--schedule"}, []string{"schedule status"}},
		{[]string{"--agents", "claude", "--schedule", "30m"}, []string{"agents install claude", "schedule install --every 30m"}},
		{[]string{"--schedule", "off", "--agents", "codex"}, []string{"schedule remove", "agents install codex"}},
		{[]string{"--json", "--agents", "cursor"}, []string{"agents install cursor"}},
		{[]string{"--agents=claude"}, []string{"agents install claude"}},
		{[]string{"--agents=all", "--remove"}, []string{"agents remove all"}},
		{[]string{"--schedule=30m"}, []string{"schedule install --every 30m"}},
		{[]string{"--schedule=off"}, []string{"schedule remove"}},
	}
	for _, c := range cases {
		calls := recordCommands(t, nil)
		code, _, _ := runFront(t, c.args...)
		if code != 0 || strings.Join(*calls, "|") != strings.Join(c.want, "|") {
			t.Errorf("%v: code %d calls %v, want %v", c.args, code, *calls, c.want)
		}
	}
}

func TestFrontHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		code, out, _ := runFront(t, flag)
		if code != 0 || !strings.Contains(out, "--agents") || !strings.Contains(out, "--schedule") || !strings.Contains(out, "agents") {
			t.Errorf("%s: code %d out %q", flag, code, out)
		}
	}
}

func TestFrontStopsAtFirstFailure(t *testing.T) {
	calls := recordCommands(t, map[string]int{"agents": 1})
	code, _, _ := runFront(t, "--agents", "claude", "--schedule", "30m")
	if code != 1 || len(*calls) != 1 {
		t.Errorf("code %d calls %v", code, *calls)
	}
}

func TestFrontFailureInOtherPositions(t *testing.T) {
	calls := recordCommands(t, map[string]int{"schedule": 2})
	code, _, _ := runFront(t, "--schedule", "bogus", "--agents", "claude")
	if code != 2 || strings.Join(*calls, "|") != "schedule install --every bogus" {
		t.Errorf("code %d calls %v", code, *calls)
	}
	calls = recordCommands(t, map[string]int{"schedule": 1})
	code, _, _ = runFront(t, "--agents", "claude", "--schedule", "30m")
	if code != 1 || len(*calls) != 2 {
		t.Errorf("code %d calls %v", code, *calls)
	}
}

func TestFrontHelpDiscardsOtherFlags(t *testing.T) {
	calls := recordCommands(t, nil)
	code, out, _ := runFront(t, "--agents", "claude", "--help", "--schedule", "30m")
	if code != 0 || len(*calls) != 0 || !strings.Contains(out, "flags:") {
		t.Errorf("code %d calls %v", code, *calls)
	}
}

func TestFrontJSONRefusesSeveralSteps(t *testing.T) {
	calls := recordCommands(t, nil)
	code, out, errs := runFront(t, "--json", "--agents", "claude", "--schedule", "30m")
	if code != 2 || len(*calls) != 0 || out != "" || errs == "" {
		t.Errorf("code %d calls %v out %q err %q", code, *calls, out, errs)
	}
}

func TestFrontHostsComeFromInstallTable(t *testing.T) {
	got := frontHosts()
	if len(got) != len(install.Hosts) {
		t.Fatalf("%v vs install.Hosts", got)
	}
	for i, h := range install.Hosts {
		if got[i] != h.Name {
			t.Errorf("%d: %s != %s", i, got[i], h.Name)
		}
	}
}

func TestFrontUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--agents", "nope"},
		{"--agents", "auto"},
		{"--bogus"},
		{"--remove"},
		{"--agents", "--remove"},
		{"--agents", "claude", "--bogus"},
		{"--agents", "claude", "--remove", "--remove"},
	} {
		calls := recordCommands(t, nil)
		code, _, errs := runFront(t, args...)
		if code != 2 || len(*calls) != 0 || errs == "" {
			t.Errorf("%v: code %d calls %v stderr %q", args, code, *calls, errs)
		}
	}
	_, _, errs := runFront(t, "--agents", "nope")
	for _, n := range []string{"claude", "codex", "cursor", "antigravity", "opencode", "pi", "all"} {
		if !strings.Contains(errs, n) {
			t.Errorf("valid name %s missing from %q", n, errs)
		}
	}
	if _, _, e := runFront(t, "--agents", "auto"); !strings.Contains(e, "auto") {
		t.Errorf("auto not mentioned: %q", e)
	}
}

func withFakeTerminal(t *testing.T, term bool, tui func(*App) int) {
	t.Helper()
	oldTerm, oldTUI := isTerminal, TUI
	isTerminal = func(*App) bool { return term }
	TUI = tui
	t.Cleanup(func() { isTerminal, TUI = oldTerm, oldTUI })
}

func TestNoArgsTUI(t *testing.T) {
	called := false
	withFakeTerminal(t, true, func(*App) int { called = true; return 7 })
	if code, _, _ := runFront(t); code != 7 || !called {
		t.Errorf("code %d called %v", code, called)
	}
}

func TestNoArgsHelpFallbacks(t *testing.T) {
	cases := map[string]struct {
		term bool
		tui  func(*App) int
		args []string
	}{
		"not a terminal": {false, func(*App) int { return 7 }, nil},
		"nil TUI":        {true, nil, nil},
	}
	for name, c := range cases {
		withFakeTerminal(t, c.term, c.tui)
		code, out, _ := runFront(t, c.args...)
		if code != 0 || !strings.Contains(out, "usage:") {
			t.Errorf("%s: code %d out %q", name, code, out)
		}
		if strings.HasPrefix(strings.TrimSpace(out), "{") {
			t.Errorf("%s: help must not be JSON: %q", name, out)
		}
	}
}

type helpDoc struct {
	Usage    string   `json:"usage"`
	Commands []string `json:"commands"`
}

func TestJSONWithoutCommandPrintsOneDocument(t *testing.T) {
	withFakeTerminal(t, true, func(*App) int { return 7 })
	var docs []helpDoc
	for _, args := range [][]string{{"--json"}, {"--json", "--help"}, {"--help", "--json"}} {
		code, out, errs := runFront(t, args...)
		if code != 0 || errs != "" {
			t.Fatalf("%v: code %d stderr %q", args, code, errs)
		}
		dec := json.NewDecoder(strings.NewReader(out))
		var d helpDoc
		if err := dec.Decode(&d); err != nil || dec.More() {
			t.Fatalf("%v: not exactly one JSON document: %v %q", args, err, out)
		}
		if !strings.Contains(d.Usage, "usage:") || len(d.Commands) == 0 {
			t.Fatalf("%v: %+v", args, d)
		}
		docs = append(docs, d)
	}
	if strings.Join(docs[0].Commands, ",") != strings.Join(docs[1].Commands, ",") || docs[0].Usage != docs[1].Usage {
		t.Fatalf("shapes differ: %+v %+v", docs[0], docs[1])
	}
}

func TestJSONHelpCommandPrintsJSON(t *testing.T) {
	code, out, _ := runFront(t, "--json", "help")
	var d helpDoc
	if code != 0 || json.Unmarshal([]byte(out), &d) != nil || len(d.Commands) == 0 {
		t.Fatalf("code %d %q", code, out)
	}
}

func TestFrontNeverTouchesExplicit(t *testing.T) {
	b, err := os.ReadFile("front.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Expl"+"icit") || strings.Contains(string(b), "eng"+"ine") {
		t.Error("front.go must not reach engine options")
	}
}

func TestUnknownCommandStillExit2(t *testing.T) {
	if code, _, _ := runFront(t, "nonesuch"); code != 2 {
		t.Errorf("code %d", code)
	}
}
