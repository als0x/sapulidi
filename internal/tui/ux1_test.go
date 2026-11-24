package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
)

const gib = int64(1) << 30

// realMachineRows is the shape the owner saw: nothing ready, 1.2 GB chosen by hand, 155 protected rows.
func realMachineRows() []Row {
	rows := []Row{
		{Path: "/home/user/old/node_modules", Bytes: gib * 12 / 10, Source: SrcOptionalOff, Category: "node", RuleKey: "node/nm@1"},
	}
	for i := 0; i < 155; i++ {
		rows = append(rows, Row{Path: "/home/user/proj/k" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + string(rune('A'+i/26)),
			Bytes: -1, Source: SrcKept, Guard: "dirty", Category: "go", RuleKey: "go/x@1"})
	}
	return rows
}

func modelOf(rows []Row, s SummaryData) Model {
	m, _ := Update(NewModel(true), ScanDoneMsg{Rows: rows, Summary: s})
	return m
}

func unknownSummary() SummaryData {
	s := placesSummary()
	s.Unknown = []Uncovered{{"/home/user/.local/share/containers", 21 * gib}, {"/home/user/proj/target", 4 * gib}, {"/home/user/.cache/x", gib}}
	return s
}

func TestHeadline(t *testing.T) {
	for _, c := range []struct {
		name string
		rows []Row
		want string
	}{
		{"real machine", realMachineRows(), "Free now: 0 B automatic | 1.2 GB if you choose | 155 protected"},
		{"ready rows", []Row{
			{Path: "/a/x", Bytes: gib, Source: SrcAuto}, {Path: "/a/y", Bytes: gib, Source: SrcOptionalOn},
			{Path: "/b/z", Bytes: gib / 2, Source: SrcOptionalOff},
		}, "Free now: 2.0 GB automatic | 512.0 MB if you choose | 0 protected"},
		{"unknown sizes", []Row{
			{Path: "/a/x", Bytes: gib, Source: SrcAuto}, {Path: "/a/y", Bytes: -1, Source: SrcAuto},
			{Path: "/b/z", Bytes: -1, Source: SrcOptionalOff},
		}, "Free now: 1.0 GB+ automatic | 0 B+ if you choose | 0 protected"},
		{"nothing", nil, "Free now: 0 B automatic | 0 B if you choose | 0 protected"},
	} {
		m := modelOf(c.rows, placesSummary())
		for _, scr := range []Model{m, flat(m)} {
			if got := View(scr, 80, 24); !strings.Contains(strings.Join(got, "\n"), c.want) {
				t.Errorf("%s: headline %q missing in\n%s", c.name, c.want, strings.Join(got, "\n"))
			}
			if got := summaryLines(scr)[0]; got != c.want {
				t.Errorf("%s: first summary line %q", c.name, got)
			}
		}
	}
}

func TestEmptyReadyMessageExactlyWhenNothingReadyAndSomethingOptional(t *testing.T) {
	got := strings.Join(View(modelOf(realMachineRows(), placesSummary()), 80, 24), "\n")
	for _, part := range []string{"nothing is ready to delete automatically;", "1.2 GB can be chosen by hand: press enter on a place"} {
		if !strings.Contains(got, part) {
			t.Errorf("message part %q missing at 80x24:\n%s", part, got)
		}
	}
	ready := append(realMachineRows(), Row{Path: "/home/user/r/go", Bytes: gib, Source: SrcAuto})
	if got := screen(modelOf(ready, placesSummary())); strings.Contains(got, "nothing is ready to delete automatically") {
		t.Errorf("a ready row exists:\n%s", got)
	}
	keptOnly := []Row{{Path: "/home/user/k", Bytes: -1, Source: SrcKept, Guard: "dirty"}}
	if got := screen(modelOf(keptOnly, placesSummary())); strings.Contains(got, "nothing is ready to delete automatically") {
		t.Errorf("no optional row exists:\n%s", got)
	}
	if got := screen(modelOf(nil, placesSummary())); strings.Contains(got, "nothing is ready to delete automatically") || !strings.Contains(got, "nothing is ready to delete") {
		t.Errorf("empty keeps its own text:\n%s", got)
	}
}

func TestLegendOnPlacesAndFlatList(t *testing.T) {
	places := strings.Join(View(placesModel(), 80, 24), "\n")
	if !strings.Contains(places, "ready = removable now | optional = you pick it | kept = protected") {
		t.Errorf("places legend:\n%s", places)
	}
	list := strings.Join(View(listModel(), 80, 24), "\n")
	if !strings.Contains(list, "auto = removable now | optional = you pick it | kept = protected") {
		t.Errorf("list legend:\n%s", list)
	}
}

func TestHelpOpensFromEveryListScreenAndReturnsToTheSameCursor(t *testing.T) {
	m := modelOf(placesRows(), unknownSummary())
	place := func() Model { x, _ := keys(m, "j<enter>"); return x }
	unk := func() Model { x, _ := keys(m, "u"); return x }
	for name, start := range map[string]Model{"places": m, "place": place(), "flat": flat(m), "uncovered": unk()} {
		start, _ = keys(start, "j")
		before := start
		opened, _ := keys(start, "?")
		if opened.Screen != ScreenHelp {
			t.Fatalf("%s: ? did not open the help (screen %v)", name, opened.Screen)
		}
		lines := View(opened, 80, 24)
		if len(lines) > 24 || !strings.Contains(strings.Join(lines, "\n"), "[~]") {
			t.Errorf("%s: help does not fit or lacks markers:\n%s", name, strings.Join(lines, "\n"))
		}
		for _, close := range []string{"<esc>", "?"} {
			back, _ := keys(opened, close)
			if back.Screen != before.Screen || back.Cursor != before.Cursor || back.Place != before.Place {
				t.Errorf("%s: %s returned to screen %v cursor %d, want %v %d", name, close, back.Screen, back.Cursor, before.Screen, before.Cursor)
			}
		}
	}
}

func TestHelpScrollsInASmallWindow(t *testing.T) {
	m, _ := keys(placesModel(), "?")
	m, _ = Update(m, ResizeMsg{Width: 80, Height: 24})
	first := View(m, 80, 24)
	if len(first) != 24 || !strings.Contains(first[23], "scroll") {
		t.Fatalf("expected a scroll footer:\n%s", strings.Join(first, "\n"))
	}
	m, _ = keys(m, "<down><down><down>")
	if View(m, 80, 24)[0] == first[0] {
		t.Error("down did not scroll the help")
	}
	m, _ = keys(m, "<esc>")
	if m.Screen != ScreenPlaces {
		t.Errorf("esc: screen %v", m.Screen)
	}
}

func TestUncoveredSummaryLineAndList(t *testing.T) {
	m := modelOf(placesRows(), unknownSummary())
	got := screen(m)
	if !strings.Contains(got, "not covered by any rule: 3 folders | 26.0 GB  (u)") {
		t.Errorf("summary line missing:\n%s", got)
	}
	m, _ = keys(m, "u")
	if m.Screen != ScreenUnknown {
		t.Fatalf("u: screen %v", m.Screen)
	}
	list := strings.Join(View(m, 80, 24), "\n")
	for _, want := range []string{"21.0 GB", "~/.local/share/containers", uncoveredNote} {
		if !strings.Contains(list, want) {
			t.Errorf("list lacks %q:\n%s", want, list)
		}
	}
	if strings.Index(list, "21.0 GB") > strings.Index(list, "4.0 GB") {
		t.Error("not sorted by size")
	}
	for _, k := range []string{" ", "x", "d", "a"} {
		n, eff := keys(m, k)
		if n.Screen != ScreenUnknown || len(eff) != 0 || !strings.HasPrefix(n.Status, "read-only") || len(n.Selected) != 0 {
			t.Errorf("%q must do nothing but say why: screen %v status %q selected %v", k, n.Screen, n.Status, n.Selected)
		}
	}
	back, _ := keys(m, "<esc>")
	if back.Screen != ScreenPlaces {
		t.Errorf("esc: screen %v", back.Screen)
	}
}

func TestUncoveredPartialAndNone(t *testing.T) {
	s := unknownSummary()
	s.UnknownPartial = true
	if got := screen(modelOf(placesRows(), s)); !strings.Contains(got, "26.0 GB partial  (u)") {
		t.Errorf("partial missing:\n%s", got)
	}
	none := modelOf(placesRows(), placesSummary())
	if got := screen(none); strings.Contains(got, "not covered by any rule") {
		t.Errorf("no line expected:\n%s", got)
	}
	n, _ := keys(none, "u")
	if n.Screen != ScreenPlaces || n.Status == "" {
		t.Errorf("u with nothing uncovered: screen %v status %q", n.Screen, n.Status)
	}
}

func TestFooterIsOneLinePerScreen(t *testing.T) {
	m := modelOf(placesRows(), unknownSummary())
	place, _ := keys(m, "j<enter>")
	help, _ := keys(m, "?")
	unk, _ := keys(m, "u")
	seen := map[string]bool{}
	for name, s := range map[string]Model{"places": m, "place": place, "flat": flat(m), "help": help, "uncovered": unk} {
		for _, sz := range [][2]int{{80, 24}, {120, 40}} {
			lines := View(s, sz[0], sz[1])
			last := lines[len(lines)-1]
			if name == "help" { // the help footer also counts the lines it scrolls through
				if !strings.Contains(last, "esc or ? close") {
					t.Errorf("help %v: footer %q", sz, last)
				}
			} else if last != footerOf(s) {
				t.Errorf("%s %v: last line %q is not the footer %q", name, sz, last, footerOf(s))
			}
			if strings.Contains(last, "\n") || width(last) > sz[0] {
				t.Errorf("%s: footer is not one line within the width: %q", name, last)
			}
		}
		seen[footerOf(s)] = true
	}
	if len(seen) < 4 {
		t.Errorf("footers should differ per screen: %v", seen)
	}
	if got := screen(m); strings.Contains(got, "keys ") && strings.Contains(got, "esc: reset") {
		t.Error("the six-line keys panel is still there")
	}
}

func TestScanFindsUncoveredFoldersAndSaysPartialWhenCancelled(t *testing.T) {
	home := testutil.TempHome(t)
	t.Setenv("HF_HOME", "")
	root := filepath.Join(home, "work", "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	d := &EngineDeps{
		Build: func() (*core.Env, error) {
			return &core.Env{Exclusions: testutil.NoExclusions, Home: home, Now: time.Now(), Roots: []core.Root{{Path: root, Source: "config"}},
				Platform: &testutil.FakePlatform{OSName: "linux", Free: 100 << 30}, Run: &testutil.FakeRunner{},
				Settings: &testutil.FakeSettings{}}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Scan(ctx, func(string, int, int) {}); err != nil {
		t.Fatal(err)
	}
	if s := d.Summary(); !s.UnknownPartial {
		t.Errorf("a cancelled scan must say partial: %+v", s)
	}
	if _, err := d.Scan(context.Background(), func(string, int, int) {}); err != nil {
		t.Fatal(err)
	}
	if s := d.Summary(); s.UnknownPartial || len(s.Unknown) != 0 {
		t.Errorf("small fixture has nothing uncovered and finishes: %+v", s)
	}
}
