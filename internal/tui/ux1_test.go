package tui

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
	"github.com/als0x/sapulidi/internal/tui/term"
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

// modelOf is a model after the scan AND its second phase: the uncovered folders of s have arrived.
func modelOf(rows []Row, s SummaryData) Model {
	m := scanned(rows, s)
	m, _ = Update(m, UncoveredMsg{Unknown: s.Unknown, Partial: s.UnknownPartial})
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
		{"real machine", realMachineRows(), "Free now: 0 B safe | 0 B recommended | 1.2 GB your call | 155 protected"},
		{"every label", []Row{
			{Path: "/a/x", Bytes: gib, Source: SrcAuto, Label: core.LabelSafe}, {Path: "/a/y", Bytes: gib, Source: SrcOptionalOn, Label: core.LabelRecommended},
			{Path: "/b/z", Bytes: gib / 2, Source: SrcOptionalOff},
		}, "1.0 GB safe | 1.0 GB rec. | 512.0 MB your call | 0 kept"},
		{"unknown sizes", []Row{
			{Path: "/a/x", Bytes: gib, Source: SrcAuto, Label: core.LabelSafe}, {Path: "/a/y", Bytes: -1, Source: SrcAuto, Label: core.LabelSafe},
			{Path: "/b/z", Bytes: -1, Source: SrcOptionalOff},
		}, "Free now: 1.0 GB+ safe | 0 B recommended | 0 B+ your call | 0 protected"},
		{"nothing", nil, "Free now: 0 B safe | 0 B recommended | 0 B your call | 0 protected"},
	} {
		m := modelOf(c.rows, placesSummary())
		for _, scr := range []Model{m, flat(m)} {
			if got := View(scr, 80, 24); !strings.Contains(strings.Join(got, "\n"), c.want) {
				t.Errorf("%s: headline %q missing in\n%s", c.name, c.want, strings.Join(got, "\n"))
			}
			if got := summaryLines(scr, 76)[0]; got != c.want {
				t.Errorf("%s: first summary line %q", c.name, got)
			}
		}
	}
}

func TestEmptyReadyMessageExactlyWhenNothingReadyAndSomethingOptional(t *testing.T) {
	got := strings.Join(View(modelOf(realMachineRows(), placesSummary()), 80, 24), "\n")
	for _, part := range []string{"nothing is safe or recommended to remove;", "1.2 GB is your call: press enter on a place"} {
		if !strings.Contains(got, part) {
			t.Errorf("message part %q missing at 80x24:\n%s", part, got)
		}
	}
	ready := append(realMachineRows(), Row{Path: "/home/user/r/go", Bytes: gib, Source: SrcAuto, Label: core.LabelSafe})
	if got := screen(modelOf(ready, placesSummary())); strings.Contains(got, "nothing is safe or recommended") {
		t.Errorf("a ready row exists:\n%s", got)
	}
	keptOnly := []Row{{Path: "/home/user/k", Bytes: -1, Source: SrcKept, Guard: "dirty"}}
	if got := screen(modelOf(keptOnly, placesSummary())); strings.Contains(got, "nothing is safe or recommended") {
		t.Errorf("no optional row exists:\n%s", got)
	}
	if got := screen(modelOf(nil, placesSummary())); strings.Contains(got, "nothing is safe or recommended") || !strings.Contains(got, "nothing to remove here") {
		t.Errorf("empty keeps its own text:\n%s", got)
	}
}

func TestLegendOnPlacesAndFlatList(t *testing.T) {
	const want = "safe, recommended: preselected | your call: you pick | protected: stays"
	places := strings.Join(View(placesModel(), 80, 24), "\n")
	if !strings.Contains(places, want) {
		t.Errorf("places legend:\n%s", places)
	}
	list := strings.Join(View(listModel(), 80, 24), "\n")
	if !strings.Contains(list, want) {
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
	if got := screen(modelOf(placesRows(), s)); !strings.Contains(got, "26.0 GB+ partial  (u)") {
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

func TestEngineDepsUncoveredIsASeparatePhaseAndPartialWhenCancelled(t *testing.T) {
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
	if _, err := d.Scan(context.Background(), func(string, int, int) {}); err != nil {
		t.Fatal(err)
	}
	if s := d.Summary(); len(s.Unknown) != 0 || s.UnknownPartial {
		t.Errorf("the scan itself must not search for uncovered folders: %+v", s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, partial := d.Uncovered(ctx); !partial {
		t.Error("a cancelled search must say partial")
	}
	if dirs, partial := d.Uncovered(context.Background()); partial || len(dirs) != 0 {
		t.Errorf("small fixture has nothing uncovered and finishes: %v %v", dirs, partial)
	}
}

// slowUncovered is a Deps whose second phase blocks until it is released or cancelled.
type slowUncovered struct {
	*fakeDeps
	release  chan struct{}
	ctxEnded chan struct{}
}

func (d *slowUncovered) Uncovered(ctx context.Context) ([]Uncovered, bool) {
	select {
	case <-ctx.Done():
		close(d.ctxEnded)
		return nil, true
	case <-d.release:
		return []Uncovered{{"/home/user/big", 5 * gib}}, false
	}
}

func slowRun(t *testing.T) (*fakeTerm, *slowUncovered, chan error) {
	ft, d := newFakes()
	s := &slowUncovered{fakeDeps: d, release: make(chan struct{}), ctxEnded: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), s) }()
	return ft, s, done
}

func TestPlacesAreUsableWhileTheUncoveredSearchRuns(t *testing.T) {
	ft, d, done := slowRun(t)
	ft.waitFor(t, "searching") // the places are on screen and the summary says the search is running
	ft.waitFor(t, "enter to look inside")
	ft.keys(" ")
	ft.waitFor(t, "y: yes") // a place with only "your call" rows asks first
	ft.keys("y")
	ft.waitFor(t, "[x]")
	ft.keys("u")
	ft.waitFor(t, "not searched yet")
	ft.special(term.KeyEsc) // cancels the search, not the plan
	ft.waitFor(t, "search cancelled")
	select {
	case <-d.ctxEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("esc did not cancel the uncovered search")
	}
	if !strings.Contains(ft.screen(), "/cache") && !strings.Contains(ft.screen(), "cache") {
		t.Errorf("the places must stay:\n%s", ft.screen())
	}
	ft.keys("q")
	if err := finish(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestQuitDuringTheUncoveredSearchCancelsIt(t *testing.T) {
	ft, d, done := slowRun(t)
	ft.waitFor(t, "searching")
	ft.keys("q")
	if err := finish(t, done); err != nil {
		t.Fatal(err)
	}
	select {
	case <-d.ctxEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("quitting did not cancel the uncovered search")
	}
}

func TestUncoveredResultArrivesAfterTheScreenShowedThePlaces(t *testing.T) {
	ft, d, done := slowRun(t)
	ft.waitFor(t, "searching")
	close(d.release)
	ft.waitFor(t, "not covered by any rule: 1 folder")
	ft.keys("q")
	if err := finish(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestLateUncoveredMessageChangesNothingOnScreen(t *testing.T) {
	late := UncoveredMsg{Unknown: []Uncovered{{"/x", gib}}}
	// after a cancel the message is dropped whole
	m, _ := keys(placesModel(), "<esc>")
	if m.UnkState != UnkCancelled {
		t.Fatalf("esc did not cancel: %v", m.UnkState)
	}
	if n, _ := Update(m, late); !reflect.DeepEqual(n, m) {
		t.Error("a message after a cancel changed the model")
	}
	// after the user left the places it only fills in the summary: screen, cursor, status and selection stay
	in, _ := keys(placesModel(), "j<enter>")
	n, _ := Update(in, late)
	if n.Screen != in.Screen || n.Cursor != in.Cursor || n.Place != in.Place || n.Status != in.Status || !reflect.DeepEqual(n.Selected, in.Selected) {
		t.Errorf("late message moved the screen: %v/%d -> %v/%d", in.Screen, in.Cursor, n.Screen, n.Cursor)
	}
	if len(n.Summary.Unknown) != 1 || n.UnkState != UnkDone {
		t.Errorf("late message was not taken: %+v %v", n.Summary.Unknown, n.UnkState)
	}
	// a second message changes nothing
	if again, _ := Update(n, UncoveredMsg{Unknown: []Uncovered{{"/y", 1}}}); !reflect.DeepEqual(again, n) {
		t.Error("a second message changed the finished model")
	}
}

func TestScanDoneAsksForTheSecondPhase(t *testing.T) {
	m, eff := Update(NewModel(true), ScanDoneMsg{Rows: placesRows(), Summary: placesSummary()})
	if m.Screen != ScreenPlaces || m.UnkState != UnkSearching {
		t.Fatalf("screen %v state %v", m.Screen, m.UnkState)
	}
	if len(eff) != 1 {
		t.Fatalf("effects %v", eff)
	}
	if _, ok := eff[0].(StartUncovered); !ok {
		t.Fatalf("effect %T", eff[0])
	}
	if !strings.Contains(screen(m), "not covered by any rule: searching...") {
		t.Errorf("summary does not say it is searching:\n%s", screen(m))
	}
	if _, eff := keys(m, "<esc>"); len(eff) != 1 {
		t.Errorf("esc must cancel the search: %v", eff)
	} else if _, ok := eff[0].(CancelUncovered); !ok {
		t.Errorf("effect %T", eff[0])
	}
}

func TestUBeforeTheSearchEndsOrAfterACancelDoesNotClaimNoneFound(t *testing.T) {
	searching := placesModel()
	n, _ := keys(searching, "u")
	if n.Screen != ScreenPlaces || !strings.Contains(n.Status, "not searched yet") || strings.Contains(n.Status, "no big folder") {
		t.Errorf("searching: screen %v status %q", n.Screen, n.Status)
	}
	cancelled, _ := keys(searching, "<esc>")
	n, _ = keys(cancelled, "u")
	if !strings.Contains(n.Status, "search cancelled") || strings.Contains(n.Status, "no big folder") {
		t.Errorf("cancelled: status %q", n.Status)
	}
	if got := screen(cancelled); !strings.Contains(got, "not covered by any rule: search cancelled") {
		t.Errorf("cancelled summary:\n%s", got)
	}
	none, _ := keys(modelOf(placesRows(), placesSummary()), "u")
	if !strings.Contains(none.Status, "no big folder is left uncovered") {
		t.Errorf("none found: status %q", none.Status)
	}
}

func TestUncoveredListChangesNothingButLeaving(t *testing.T) {
	m, _ := keys(modelOf(placesRows(), unknownSummary()), "<down>u")
	for _, k := range []string{"<enter>", "y", "n", "c", "g", "o", "s", "/", " ", "x", "d", "a", "<bs>"} {
		n, eff := keys(m, k)
		n.Status = m.Status
		if len(eff) != 0 || !reflect.DeepEqual(n, m) {
			t.Errorf("%q on the uncovered list changed the model or asked for %v", k, eff)
		}
	}
}

func TestPartialSearchSizesAreLowerBounds(t *testing.T) {
	s := unknownSummary()
	s.UnknownPartial = true
	m, _ := keys(modelOf(placesRows(), s), "u")
	got := strings.Join(View(m, 80, 24), "\n")
	for _, want := range []string{"Not covered by any rule: 3 folders, 26.0 GB+ (partial)", "21.0 GB+", "4.0 GB+", "1.0 GB+"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing:\n%s", want, got)
		}
	}
	done, _ := keys(modelOf(placesRows(), unknownSummary()), "u")
	if got := strings.Join(View(done, 80, 24), "\n"); strings.Contains(got, "+") && strings.Contains(got, "GB+") {
		t.Errorf("a finished search has exact sizes:\n%s", got)
	}
}

func TestUncoveredScreenIsNotCutAt80Columns(t *testing.T) {
	s := placesSummary()
	for i := 0; i < 20; i++ {
		s.Unknown = append(s.Unknown, Uncovered{Path: "/home/user/.cache/folder-" + string(rune('a'+i)), Bytes: int64(20-i) * gib})
	}
	s.UnknownPartial = true
	m, _ := keys(modelOf(placesRows(), s), "u")
	m, _ = keys(m, " ")
	lines := View(m, 80, 24)
	got := strings.Join(lines, "\n")
	for _, want := range []string{"(partial)", uncoveredNote, "read-only: nothing here can be removed", "up/down scroll | esc back | ? help | q quit"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q cut or missing:\n%s", want, got)
		}
	}
	if len(lines) > 24 {
		t.Errorf("%d lines", len(lines))
	}
	for _, l := range lines {
		if width(l) > 80 {
			t.Errorf("line too wide: %q", l)
		}
	}
}

func TestHelpAndUncoveredFootersAdvertiseOnlyWhatWorks(t *testing.T) {
	m := modelOf(placesRows(), unknownSummary())
	for name, start := range map[string]Model{"help": mustKeys(m, "?"), "uncovered": mustKeys(m, "u")} {
		for _, k := range []string{"<esc>", "?", "q"} {
			if k == "?" && name == "uncovered" {
				if n, _ := keys(start, k); n.Screen != ScreenHelp {
					t.Errorf("uncovered: ? must open help")
				}
				continue
			}
			n, eff := keys(start, k)
			if k == "q" {
				if len(eff) != 1 {
					t.Errorf("%s: q must quit", name)
				} else if _, ok := eff[0].(Quit); !ok {
					t.Errorf("%s: q gave %T", name, eff[0])
				}
				continue
			}
			if n.Screen == start.Screen {
				t.Errorf("%s: %s must leave the screen", name, k)
			}
		}
	}
	for _, want := range []string{"esc or ? close", "q quit"} {
		if f := footerOf(mustKeys(m, "?")); !strings.Contains(f, want) {
			t.Errorf("help footer %q lacks %q", f, want)
		}
	}
}

func mustKeys(m Model, s string) Model { n, _ := keys(m, s); return n }

func helpText80(t *testing.T) string {
	t.Helper()
	return strings.Join(helpText, "\n")
}

func TestHelpClaimsMatchTheKeys(t *testing.T) {
	help := helpText80(t)
	for _, line := range helpText {
		if width(line) > 76 {
			t.Errorf("help line too wide: %q", line)
		}
	}
	// a: on the places only the ready rows of the places in view
	const optOff = "/home/user/old/node_modules"
	places := mustKeys(placesModel(), "a")
	if places.Selected[optOff] || len(places.Selected) == 0 {
		t.Errorf("places a: %v", places.Selected)
	}
	// a: inside a place the ready rows only
	inOld := mustKeys(placesModel(), "jj<enter>a")
	if inOld.Place != "~/old/" || inOld.Selected[optOff] {
		t.Errorf("place a selected an optional row: %v in %q", inOld.Selected, inOld.Place)
	}
	// a: in the flat list the safe and recommended rows in view, never a your call one, shown by o or not
	for _, script := range []string{"a", "oa"} {
		if list := mustKeys(flat(placesModel()), script); list.Selected[optOff] || len(list.Selected) == 0 {
			t.Errorf("flat %s: %v", script, list.Selected)
		}
	}
	// o: the flat list toggles, the places say they always list them
	if !mustKeys(flat(placesModel()), "o").ShowOptional {
		t.Error("o must show optional rows in the flat list")
	}
	if got := mustKeys(placesModel(), "o"); got.ShowOptional || !strings.Contains(got.Status, "always listed") {
		t.Errorf("o on the places: %v %q", got.ShowOptional, got.Status)
	}
	// x: excludes the row under the cursor in the flat list and in a place; the places say to open one
	for name, m := range map[string]Model{"flat": flat(placesModel()), "place": mustKeys(placesModel(), "<enter>")} {
		if _, eff := keys(m, "x"); len(eff) != 1 {
			t.Errorf("%s: x must exclude the row under the cursor: %v", name, eff)
		} else if _, ok := eff[0].(AddExclusion); !ok {
			t.Errorf("%s: x gave %T", name, eff[0])
		}
	}
	if got := mustKeys(placesModel(), "x"); !strings.Contains(got.Status, "open the place") {
		t.Errorf("x on the places: %q", got.Status)
	}
	for _, claim := range []string{
		"select the safe and recommended rows in view, never your call",
		"a never deselects, space does",
		"At start every safe and recommended row is selected.",
		"flat list: show or hide the rows of switched off categories",
		"exclude the row under the cursor (flat list, inside a place)",
		"on the places, open a place first",
		"u      folders that no rule covers",
		"q quit",
	} {
		if !strings.Contains(help, claim) {
			t.Errorf("help lacks %q", claim)
		}
	}
}
