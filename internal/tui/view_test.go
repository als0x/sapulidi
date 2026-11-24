package tui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Regenerate the golden screens with: go test ./internal/tui -update
var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// sgr matches the colour codes the view itself emits, and nothing else.
var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

// width counts terminal columns of the raw line: nothing is stripped, so a stray escape sequence is counted.
func width(s string) int { return cols(s) }

func scanningModel() Model {
	m := NewModel(true)
	for _, p := range []ScanProgressMsg{{"go", 10, 10}, {"node", 3, 12}, {"docker", 0, 4}} {
		m, _ = Update(m, p)
	}
	return m
}

func pausedModel() Model {
	s := fixtureSummary()
	s.Paused = true
	m, _ := Update(NewModel(true), ScanDoneMsg{Rows: fixtureRows(), Summary: s})
	return flat(m)
}

func resultModel() Model {
	m, _ := keys(listModel(), " dy")
	m, _ = Update(m, DeleteDoneMsg{Result: Result{
		Freed: 3_100_000_000, Removed: []string{m.Confirm[0]},
		Kept: []KeptItem{{"/home/user/proj/node_modules", "open: a process has files open here"}},
	}})
	return m
}

// emptyModel has nothing ready to delete, one optional row off and one kept row.
func emptyModel() Model {
	rows := []Row{
		{Path: "/home/user/old/node_modules", Bytes: 800_000_000, Source: SrcOptionalOff, Category: "node", RuleKey: "node/node-modules@1"},
		{Path: "/home/user/work/repo/.venv", Bytes: -1, Source: SrcKept, Guard: "dirty", Category: "python", RuleKey: "python/venv@1"},
	}
	m, _ := Update(NewModel(true), ScanDoneMsg{Rows: rows, Summary: fixtureSummary()})
	return flat(m)
}

func script(s string) func() Model {
	return func() Model { m, _ := keys(listModel(), s); return m }
}

func cursorOn(path string) func() Model {
	return func() Model {
		m := listModel()
		for i, r := range m.visible() {
			if r.Path == path {
				m.Cursor = i
			}
		}
		return m
	}
}

var goldenCases = []struct {
	name  string
	model func() Model
}{
	{"scanning", scanningModel},
	{"list", listModel},
	{"list_optional", script("o")},
	{"search", script("/node")},
	{"sort_age", script("s")},
	{"sort_updated", script("ss")},
	{"sort_path", script("sss")},
	{"detail", script("<enter>")},
	{"confirm", script("oad")},
	{"result", resultModel},
	{"excluded_row", cursorOn("/home/user/proj2/target")},
	{"kept_row", func() Model {
		m, _ := keys(listModel(), "g")
		for i, r := range m.visible() {
			if r.Path == "/home/user/work/repo/.venv" {
				m.Cursor = i
			}
		}
		return m
	}},
	{"list_guarded", script("g")},
	{"list_empty", emptyModel},
	{"wholesale_row", cursorOn("/home/user/.npm/_cacache")},
	{"paused", pausedModel},
	{"help", script("?")},
	{"help_places", func() Model { m, _ := keys(placesModel(), "?"); return m }},
	{"uncovered", func() Model { m, _ := keys(modelOf(placesRows(), unknownSummary()), "u"); return m }},
	{"places_uncovered", func() Model { return modelOf(placesRows(), unknownSummary()) }},
	{"places_nothing_ready", func() Model { return modelOf(realMachineRows(), placesSummary()) }},
}

func TestGoldenScreens(t *testing.T) {
	for _, c := range goldenCases {
		for _, sz := range [][2]int{{80, 24}, {120, 40}} {
			name := fmt.Sprintf("%s_%dx%d", c.name, sz[0], sz[1])
			t.Run(name, func(t *testing.T) {
				got := strings.Join(View(c.model(), sz[0], sz[1]), "\n") + "\n"
				file := filepath.Join("testdata", name+".golden")
				if *update {
					if err := os.MkdirAll("testdata", 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(file)
				if err != nil {
					t.Fatalf("%v (run go test ./internal/tui -update)", err)
				}
				if got != string(want) {
					t.Fatalf("screen differs from %s:\n%s", file, got)
				}
			})
		}
	}
}

func TestGoldenTinyWindow(t *testing.T) {
	for _, sz := range [][2]int{{79, 24}, {80, 23}, {40, 10}} {
		name := fmt.Sprintf("tiny_%dx%d", sz[0], sz[1])
		got := strings.Join(View(listModel(), sz[0], sz[1]), "\n") + "\n"
		file := filepath.Join("testdata", name+".golden")
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(file)
		if err != nil || got != string(want) {
			t.Fatalf("%s differs (err %v):\n%s", file, err, got)
		}
		if !strings.Contains(got, "make the window larger") {
			t.Fatalf("no message in %s", name)
		}
	}
}

func TestPlainHasNoColourOrBoxCharacters(t *testing.T) {
	for _, c := range goldenCases {
		for _, l := range View(c.model(), 120, 40) {
			for _, r := range l {
				if r > 126 || r == 27 {
					t.Fatalf("%s: non-ASCII %q in %q", c.name, r, l)
				}
			}
		}
	}
}

func TestColourModeStaysWithinWidth(t *testing.T) {
	m := listModel()
	m.Plain = false
	lines := View(m, 80, 24)
	var coloured bool
	for _, l := range lines {
		coloured = coloured || strings.Contains(l, "\x1b[")
		checkLine(t, "colour", l, 80, false)
	}
	if !coloured {
		t.Fatal("expected colour codes")
	}
}

func TestViewNeverExceedsTheWindow(t *testing.T) {
	models := map[string]Model{"scanning": scanningModel()}
	for _, c := range goldenCases {
		models[c.name] = c.model()
	}
	long := listModel()
	long.Rows = append(long.Rows, Row{Path: "/" + strings.Repeat("deep/", 60) + "x", Bytes: 1, Source: SrcAuto, Members: []string{"docker:" + strings.Repeat("c", 120)}})
	models["long"] = long
	models["long_detail"], _ = keys(long, "<enter>")
	for name, m := range models {
		for w := 1; w <= 200; w += 3 {
			for h := 1; h <= 60; h += 5 {
				lines := View(m, w, h)
				if len(lines) > h {
					t.Fatalf("%s %dx%d: %d lines", name, w, h, len(lines))
				}
				for _, l := range lines {
					if width(l) > w {
						t.Fatalf("%s %dx%d: line of %d columns: %q", name, w, h, width(l), l)
					}
				}
			}
		}
	}
	for w := 80; w <= 200; w++ {
		for h := 24; h <= 60; h++ {
			for name, m := range models {
				for _, l := range View(m, w, h) {
					if width(l) > w {
						t.Fatalf("%s %dx%d: line of %d columns", name, w, h, width(l))
					}
				}
			}
		}
	}
}

func TestViewIsDeterministic(t *testing.T) {
	a := strings.Join(View(listModel(), 100, 30), "\n")
	b := strings.Join(View(listModel(), 100, 30), "\n")
	if a != b {
		t.Fatal("View differs between calls")
	}
}

func TestFormatters(t *testing.T) {
	cases := map[string]string{
		fmtBytes(0):                    "0 B",
		fmtBytes(1536):                 "1.5 KB",
		fmtBytes(3_100_000_000):        "2.9 GB",
		fmtBytes(-1):                   "?",
		fmtAge(day(2026, 11, 20), now): "4d",
		fmtAge(day(2026, 5, 24), now):  "6mo",
		fmtAge(day(2023, 1, 1), now):   "3y",
		shorten("/aaaa/bbbb/cccc", 9):  "/aa...ccc",
		fmtInterval(30 * 60e9):         "30m",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
