package tui

import (
	"reflect"
	"strings"
	"testing"
)

const (
	pNVM  = "~/.nvm/versions/node/"
	pProj = "~/proj/"
	pOld  = "~/old/"
	pEx   = "~/ex/"
	pKept = "~/keptonly/"
	pCmd  = "commands · docker"
)

func placesSummary() SummaryData {
	s := fixtureSummary()
	s.Roots = []string{"/home/user/proj"}
	s.Home = "/home/user"
	return s
}

func placesRows() []Row {
	return []Row{
		{Path: "/home/user/.nvm/versions/node/v16.20.2", Bytes: 500_000_000, Created: day(2025, 1, 1), Updated: day(2026, 1, 1), Source: SrcAuto, Category: "node", RuleKey: "node/nvm@1"},
		{Path: "/home/user/.nvm/versions/node/v18.0.0", Bytes: 300_000_000, Created: day(2025, 2, 1), Updated: day(2026, 2, 1), Source: SrcAuto, Category: "node", RuleKey: "node/nvm@1"},
		{Path: "/home/user/.nvm/versions/node/v24.22.1", Bytes: -1, Updated: day(2026, 11, 20), Source: SrcKept, Guard: "active", Category: "node", RuleKey: "node/nvm@1"},
		{Path: "/home/user/proj/node_modules", Bytes: 1_200_000_000, Created: day(2026, 3, 2), Updated: day(2026, 11, 1), Source: SrcOptionalOn, Category: "node", RuleKey: "node/nm@1"},
		{Path: "/home/user/proj/target", Bytes: 700_000_000, Updated: day(2026, 9, 9), Source: SrcExcluded, Base: SrcAuto, Category: "rust", RuleKey: "rust/target@1"},
		{Path: "/home/user/proj/.worktrees/a1", Bytes: -1, Updated: day(2026, 11, 21), Source: SrcKept, Guard: "dirty", Category: "git", RuleKey: "git/wt@1"},
		{Path: "/home/user/old/node_modules", Bytes: 800_000_000, Created: day(2024, 5, 1), Updated: day(2025, 2, 3), Source: SrcOptionalOff, Category: "node", RuleKey: "node/nm@1"},
		{Path: "/home/user/keptonly/.venv", Bytes: -1, Updated: day(2026, 11, 20), Source: SrcKept, Guard: "dirty", Category: "python", RuleKey: "python/venv@1"},
		{Path: "/home/user/ex/target", Bytes: 5_000_000, Source: SrcExcluded, Base: SrcAuto, Category: "rust", RuleKey: "rust/target@1"},
		{Path: "docker system prune", Bytes: 50_000_000, Source: SrcAuto, Category: "docker", RuleKey: "docker/prune@1", Block: containersReason},
	}
}

func placesModel() Model {
	m, _ := Update(NewModel(true), ScanDoneMsg{Rows: placesRows(), Summary: placesSummary()})
	return m
}

func placeNames(m Model) []string {
	out := []string{}
	for _, p := range m.places() {
		out = append(out, p.Name)
	}
	return out
}

func cursorPlaceName(t *testing.T, m Model) string {
	t.Helper()
	p, ok := m.cursorPlace()
	if !ok {
		t.Fatal("no place under the cursor")
	}
	return p.Name
}

func TestPlaceOf(t *testing.T) {
	home := "/home/user"
	roots := []string{"/a/b", "/a/b/c/d", "/home/user/proj/", "/a/x"}
	for _, c := range []struct{ path, want string }{
		{"/a/b/c", "/a/b/"},           // a root is a prefix
		{"/a/bc", "/a/"},              // not a path-segment prefix: the parent directory
		{"/a/b", "/a/b/"},             // the root itself
		{"/a/b/c/d/e/f", "/a/b/c/d/"}, // nested roots: the longest wins
		{"/a/b/c/other", "/a/b/"},     // under the outer root only
		{"/z/y/z", "/z/y/"},           // no root: the parent directory
		{"/home/user/.nvm/versions/node/v16.20.2", "~/.nvm/versions/node/"},
		{"/home/user/.rustup/toolchains/stable", "~/.rustup/toolchains/"},
		{"/home/user/proj/target/debug", "~/proj/"}, // a root given with a trailing slash
		{"/home/user/proj/.worktrees/a1", "~/proj/"},
		{"/home/user/thing", "~/"},  // directly in home
		{"/a/b/", "/a/b/"},          // trailing slash on the path
		{"docker system prune", ""}, // no path: the row wrapper names it
		{"relative/dir", ""},
	} {
		if got := placeOf(c.path, roots, home); got != c.want {
			t.Errorf("placeOf(%q) = %q, want %q", c.path, got, c.want)
		}
	}
	if got := placeOfRow(Row{Path: "docker system prune", Category: "docker"}, nil, home); got != pCmd {
		t.Errorf("path-less row: %q", got)
	}
	if got := placeOf("/hx/thing", nil, "/h"); got != "/hx/" { // home is a segment prefix too
		t.Errorf("home sibling: %q", got)
	}
	if got := placeOf("/home/user/x", nil, ""); got != "/home/user/" {
		t.Errorf("no home: %q", got)
	}
}

func TestRowsGetTheirPlaceFromTheScan(t *testing.T) {
	m := placesModel()
	got := map[string]string{}
	for _, r := range m.Rows {
		got[r.Path] = r.Place
	}
	for path, want := range map[string]string{
		"/home/user/.nvm/versions/node/v16.20.2": pNVM,
		"/home/user/proj/.worktrees/a1":          pProj,
		"/home/user/old/node_modules":            pOld,
		"docker system prune":                    pCmd,
	} {
		if got[path] != want {
			t.Errorf("%s: place %q, want %q", path, got[path], want)
		}
	}
}

func TestPlacesVisibilityAndSorts(t *testing.T) {
	m := placesModel()
	if m.Screen != ScreenPlaces {
		t.Fatalf("start screen %v", m.Screen)
	}
	// a kept-only place is hidden; optional-only, excluded-only and command places are listed
	if got, want := placeNames(m), []string{pProj, pNVM, pOld, pCmd, pEx}; !reflect.DeepEqual(got, want) {
		t.Fatalf("size order = %v, want %v", got, want)
	}
	g, _ := keys(m, "g")
	if got := placeNames(g); len(got) != 6 || !strings.Contains(strings.Join(got, "|"), pKept) {
		t.Fatalf("with g: %v", got)
	}
	back, _ := keys(g, "g")
	if len(placeNames(back)) != 5 {
		t.Fatalf("second g must hide the kept-only place: %v", placeNames(back))
	}
	u, _ := keys(m, "s")
	if got, want := placeNames(u), []string{pOld, pNVM, pProj, pCmd, pEx}; !reflect.DeepEqual(got, want) {
		t.Fatalf("updatedAt order = %v, want %v", got, want)
	}
	p, _ := keys(m, "ss")
	if got, want := placeNames(p), []string{pCmd, pNVM, pEx, pOld, pProj}; !reflect.DeepEqual(got, want) {
		t.Fatalf("path order = %v, want %v", got, want)
	}
	if again, _ := keys(m, "sss"); again.Sort != SortSize {
		t.Fatalf("the places sort cycles through three keys, got %v", again.Sort)
	}
	f, _ := keys(m, "/nvm<enter>")
	if got := placeNames(f); !reflect.DeepEqual(got, []string{pNVM}) {
		t.Fatalf("search: %v", got)
	}
}

func TestPlacesCursorStartsOnFirstSelectablePlace(t *testing.T) {
	m := placesModel()
	if got := cursorPlaceName(t, m); got != pProj {
		t.Fatalf("cursor on %q", got)
	}
	p, _ := keys(m, "ss<esc>") // path order starts with a place nothing can be selected in
	if got := cursorPlaceName(t, p); got != pNVM || p.Cursor != 1 {
		t.Fatalf("cursor on %q (%d)", got, p.Cursor)
	}
}

func TestSpaceOnAPlaceSelectsReadyRowsOnly(t *testing.T) {
	m, _ := keys(placesModel(), " ")
	if !reflect.DeepEqual(m.Selected, map[string]bool{"/home/user/proj/node_modules": true}) {
		t.Fatalf("selected %v", m.Selected)
	}
	if mark := placeMark(m.places()[0]); mark != "[x]" {
		t.Fatalf("mark %q", mark)
	}
	m, _ = keys(m, " ")
	if len(m.Selected) != 0 {
		t.Fatalf("second space must deselect: %v", m.Selected)
	}
	// the nvm place holds two ready rows and a kept one
	m, _ = keys(placesModel(), "j ")
	if len(m.Selected) != 2 || m.Selected["/home/user/.nvm/versions/node/v24.22.1"] {
		t.Fatalf("nvm: %v", m.Selected)
	}
	if mark := placeMark(m.places()[1]); mark != "[x]" {
		t.Fatalf("mark %q", mark)
	}
	// optional-only place: says so, changes nothing
	m, _ = keys(placesModel(), "jj ")
	if len(m.Selected) != 0 || m.Status != "only optional rows here: open it and pick them" {
		t.Fatalf("optional-only: %v %q", m.Selected, m.Status)
	}
	// kept-only place (visible with g)
	m, _ = keys(placesModel(), "g")
	for i, p := range m.places() {
		if p.Name == pKept {
			m.Cursor = i
		}
	}
	m, _ = keys(m, " ")
	if len(m.Selected) != 0 || m.Status != "only kept rows here" {
		t.Fatalf("kept-only: %v %q", m.Selected, m.Status)
	}
	// a partly selected place shows [~]
	part, _ := keys(placesModel(), "j<enter> ")
	if mark := placeMark(part.places()[1]); mark != "[~]" {
		t.Fatalf("mark %q", mark)
	}
}

func TestAOnPlacesTogglesReadyRowsOfThePlacesInView(t *testing.T) {
	m, _ := keys(placesModel(), "a")
	want := map[string]bool{"/home/user/proj/node_modules": true, "/home/user/.nvm/versions/node/v16.20.2": true, "/home/user/.nvm/versions/node/v18.0.0": true}
	if !reflect.DeepEqual(m.Selected, want) {
		t.Fatalf("selected %v", m.Selected)
	}
	m, _ = keys(m, "a")
	if len(m.Selected) != 0 {
		t.Fatalf("a again must deselect: %v", m.Selected)
	}
	f, _ := keys(placesModel(), "/nvm<enter>a")
	if len(f.Selected) != 2 {
		t.Fatalf("a only touches the places in view: %v", f.Selected)
	}
}

func TestOnPlacesOnlyShowsAStatus(t *testing.T) {
	m, _ := keys(placesModel(), "o")
	if m.ShowOptional || !strings.Contains(m.Status, "always listed") {
		t.Fatalf("o: %v %q", m.ShowOptional, m.Status)
	}
	in, _ := keys(placesModel(), "<enter>o")
	if in.ShowOptional || !strings.Contains(in.Status, "always listed") {
		t.Fatalf("o in a place: %v %q", in.ShowOptional, in.Status)
	}
}

func TestInsideAPlace(t *testing.T) {
	m, _ := keys(placesModel(), "<enter>")
	if m.Screen != ScreenPlace || m.Place != pProj {
		t.Fatalf("screen %v place %q", m.Screen, m.Place)
	}
	if got, want := pathsOf(m.visible()), []string{"/home/user/proj/node_modules", "/home/user/proj/target"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	g, _ := keys(m, "g")
	if len(g.visible()) != 3 {
		t.Fatalf("g adds the kept row: %v", pathsOf(g.visible()))
	}
	// optional rows are always listed and never preselected
	o, _ := keys(placesModel(), "jj<enter>")
	if got := pathsOf(o.visible()); !reflect.DeepEqual(got, []string{"/home/user/old/node_modules"}) || len(o.Selected) != 0 {
		t.Fatalf("optional place: %v %v", got, o.Selected)
	}
	o, _ = keys(o, " ")
	if !o.Selected["/home/user/old/node_modules"] {
		t.Fatal("space selects an optional row inside its place")
	}
	x, eff := keys(m, "jx")
	if len(eff) != 1 || x.Screen != ScreenPlace {
		t.Fatalf("x in a place: %v", eff)
	}
}

func TestNavigationReturnsToWhereItCameFrom(t *testing.T) {
	m, _ := keys(placesModel(), "<enter>")
	m, _ = keys(m, "<enter>")
	if m.Screen != ScreenDetail {
		t.Fatalf("screen %v", m.Screen)
	}
	m, _ = keys(m, "<esc>")
	if m.Screen != ScreenPlace || m.Place != pProj {
		t.Fatalf("detail esc: %v %q", m.Screen, m.Place)
	}
	m, _ = keys(m, " d")
	if m.Screen != ScreenConfirm {
		t.Fatalf("screen %v", m.Screen)
	}
	m, _ = keys(m, "n")
	if m.Screen != ScreenPlace {
		t.Fatalf("confirm n: %v", m.Screen)
	}
	m, _ = keys(m, "<enter>d") // confirm from the detail
	if m.Screen != ScreenConfirm {
		t.Fatalf("screen %v", m.Screen)
	}
	m, _ = keys(m, "<esc>")
	if m.Screen != ScreenPlace {
		t.Fatalf("detail->confirm esc: %v", m.Screen)
	}
	m, _ = keys(m, "<esc>")
	if m.Screen != ScreenPlaces || cursorPlaceName(t, m) != pProj {
		t.Fatalf("esc in a place: %v", m.Screen)
	}
	// from the places: confirm returns to the places
	m, _ = keys(m, "dn")
	if m.Screen != ScreenPlaces {
		t.Fatalf("places confirm n: %v", m.Screen)
	}
	m, _ = keys(m, "<esc>")
	if m.Screen != ScreenPlaces {
		t.Fatalf("esc on the places stays: %v", m.Screen)
	}
	if _, eff := keys(m, "q"); !reflect.DeepEqual(eff, []Effect{Quit{}}) {
		t.Fatalf("q on the places: %v", eff)
	}
	p, _ := keys(placesModel(), "<enter>")
	if _, eff := keys(p, "q"); !reflect.DeepEqual(eff, []Effect{Quit{}}) {
		t.Fatalf("q in a place: %v", eff)
	}
}

func TestPlaceSearchSurvivesOpeningAPlace(t *testing.T) {
	m, _ := keys(placesModel(), "/nvm<enter><enter>")
	if m.Screen != ScreenPlace || m.Filter != "" || len(m.visible()) != 2 {
		t.Fatalf("in the place: %v %q %d", m.Screen, m.Filter, len(m.visible()))
	}
	m, _ = keys(m, "<esc>")
	if m.Screen != ScreenPlaces || m.Filter != "nvm" || !reflect.DeepEqual(placeNames(m), []string{pNVM}) {
		t.Fatalf("back on the places: %v %q %v", m.Screen, m.Filter, placeNames(m))
	}
}

func TestCTogglesPlacesAndFlatListAndKeepsTheSelection(t *testing.T) {
	m, _ := keys(placesModel(), "a")
	n := len(m.Selected)
	m, _ = keys(m, "c")
	if m.Screen != ScreenList || len(m.Selected) != n {
		t.Fatalf("flat: %v %d", m.Screen, len(m.Selected))
	}
	m, _ = keys(m, "c")
	if m.Screen != ScreenPlaces || len(m.Selected) != n {
		t.Fatalf("places: %v %d", m.Screen, len(m.Selected))
	}
	in, _ := keys(placesModel(), "<enter>c")
	if in.Screen != ScreenList {
		t.Fatalf("c in a place: %v", in.Screen)
	}
}

func TestDeleteThatEmptiesThePlaceLandsOnThePlaces(t *testing.T) {
	m, _ := keys(placesModel(), "j<enter>ady")
	if m.Screen != ScreenResult || !m.Result.Pending {
		t.Fatalf("setup: %v %v", m.Screen, m.Result.Pending)
	}
	m, _ = Update(m, DeleteDoneMsg{Result: Result{Removed: m.Confirm}})
	m, _ = keys(m, "<esc>")
	if m.Screen != ScreenPlaces {
		t.Fatalf("an emptied place must land on the places, got %v", m.Screen)
	}
	for _, p := range placeNames(m) {
		if p == pNVM {
			t.Fatal("the emptied place is still listed")
		}
	}
	// a delete that leaves rows returns to the place
	n, _ := keys(placesModel(), "<enter>")
	n, _ = keys(n, " dy")
	n, _ = Update(n, DeleteDoneMsg{Result: Result{Removed: n.Confirm}})
	n, _ = keys(n, "<esc>")
	if n.Screen != ScreenPlace || n.Place != pProj {
		t.Fatalf("the place still has an excluded row: %v %q", n.Screen, n.Place)
	}
}

func TestSelectionFromOneViewIsDeletedFromAnother(t *testing.T) {
	m, _ := keys(placesModel(), "a")
	m, _ = keys(m, "/ex<enter>d") // the search hides every selected place
	want := []string{"/home/user/proj/node_modules", "/home/user/.nvm/versions/node/v16.20.2", "/home/user/.nvm/versions/node/v18.0.0"}
	if m.Screen != ScreenConfirm || !reflect.DeepEqual(m.Confirm, want) {
		t.Fatalf("confirm %v", m.Confirm)
	}
	text := strings.Join(View(m, 100, 30), "\n")
	for _, p := range want {
		if !strings.Contains(text, p) {
			t.Fatalf("the confirmation hides %s:\n%s", p, text)
		}
	}
}

func TestOptionalRowsStaySelectedWhenOGoesOff(t *testing.T) {
	m, _ := keys(listModel(), "oa")
	if !m.Selected["/home/user/old/node_modules"] {
		t.Fatal("setup")
	}
	m, _ = keys(m, "o")
	if !m.Selected["/home/user/old/node_modules"] {
		t.Fatal("o off must not unselect")
	}
	if s := screen(m); !strings.Contains(s, "of") || !strings.Contains(s, "selected") {
		t.Fatalf("summary:\n%s", s)
	}
	m, _ = keys(m, "d")
	if m.Screen != ScreenConfirm || !strings.Contains(strings.Join(View(m, 100, 30), "\n"), "/home/user/old/node_modules") {
		t.Fatal("the confirmation must list the hidden optional row")
	}
}

func TestRescanDropsSelectedPathsThatAreNoLongerSelectable(t *testing.T) {
	m, _ := keys(placesModel(), "a")
	rows := placesRows()
	rows[0].Source = SrcKept
	m, _ = Update(m, ScanDoneMsg{Rows: rows, Summary: placesSummary()})
	if m.Selected["/home/user/.nvm/versions/node/v16.20.2"] || !m.Selected["/home/user/.nvm/versions/node/v18.0.0"] {
		t.Fatalf("selected %v", m.Selected)
	}
}

func emptyPlacesModel() Model {
	rows := []Row{{Path: "/home/user/work/repo/.venv", Bytes: -1, Source: SrcKept, Guard: "dirty", Category: "python", RuleKey: "python/venv@1"}}
	m, _ := Update(NewModel(true), ScanDoneMsg{Rows: rows, Summary: placesSummary()})
	return m
}

func init() {
	open := func(keysPressed string) func() Model {
		return func() Model { m, _ := keys(placesModel(), keysPressed); return m }
	}
	goldenCases = append(goldenCases, []struct {
		name  string
		model func() Model
	}{
		{"places", placesModel},
		{"place_open", open("<enter>")},
		{"place_optional", open("jj<enter>")},
		{"places_guarded", open("g")},
		{"places_selected", open("ajj ")},
		{"places_empty", emptyPlacesModel},
	}...)
}
