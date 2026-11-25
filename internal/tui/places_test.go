package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/core"
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
		{Path: "/home/user/proj/node_modules", Bytes: 1_200_000_000, Created: day(2026, 3, 2), Updated: day(2026, 11, 1), Source: SrcOptionalOn, Label: core.LabelRecommended, Category: "node", RuleKey: "node/nm@1"},
		{Path: "/home/user/proj/target", Bytes: 700_000_000, Updated: day(2026, 9, 9), Source: SrcExcluded, Base: SrcAuto, Category: "rust", RuleKey: "rust/target@1"},
		{Path: "/home/user/proj/.worktrees/a1", Bytes: -1, Updated: day(2026, 11, 21), Source: SrcKept, Guard: "dirty", Category: "git", RuleKey: "git/wt@1"},
		{Path: "/home/user/old/node_modules", Bytes: 800_000_000, Created: day(2024, 5, 1), Updated: day(2025, 2, 3), Source: SrcOptionalOff, Category: "node", RuleKey: "node/nm@1"},
		{Path: "/home/user/keptonly/.venv", Bytes: -1, Updated: day(2026, 11, 20), Source: SrcKept, Guard: "dirty", Category: "python", RuleKey: "python/venv@1"},
		{Path: "/home/user/ex/target", Bytes: 5_000_000, Source: SrcExcluded, Base: SrcAuto, Category: "rust", RuleKey: "rust/target@1"},
		{Path: "docker system prune", Bytes: 50_000_000, Source: SrcAuto, Category: "docker", RuleKey: "docker/prune@1", Block: containersReason},
	}
}

func placesModel() Model { return scanned(placesRows(), placesSummary()) }

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
		if got := placeOf(c.path, roots, nil, home); got != c.want {
			t.Errorf("placeOf(%q) = %q, want %q", c.path, got, c.want)
		}
	}
	if got := placeOfRow(Row{Path: "docker system prune", Category: "docker"}, nil, nil, home); got != pCmd {
		t.Errorf("path-less row: %q", got)
	}
	if got := placeOf("/hx/thing", nil, nil, "/h"); got != "/hx/" { // home is a segment prefix too
		t.Errorf("home sibling: %q", got)
	}
	if got := placeOf("/home/user/x", nil, nil, ""); got != "/home/user/" {
		t.Errorf("no home: %q", got)
	}
}

func TestPlaceOfBases(t *testing.T) {
	home := "/home/user"
	roots := []string{"/tmp/claude-1000/-proj-rooted"}
	bases := []string{"/tmp/claude-1000", "/tmp/claude-1000/deep/", "/a/b", "/var/folders/xx/T", "", "rel/base", "/tmp/claude-1000"}
	for _, c := range []struct{ path, want string }{
		{"/tmp/claude-1000/-Users-x-proj/0a1b2c", "/tmp/claude-1000/"},
		{"/tmp/claude-1000/-Users-y-other/3d4e5f", "/tmp/claude-1000/"},
		{"/tmp/claude-1000/-proj-rooted/s1", "/tmp/claude-1000/-proj-rooted/"}, // root beats base
		{"/tmp/claude-1000", "/tmp/"},                                          // the base itself: parent rule
		{"/tmp/claude-1000/deep/x/y", "/tmp/claude-1000/deep/"},                // nested bases: longest wins
		{"/a/bc/x", "/a/bc/"}, // /a/b is not a segment prefix of /a/bc
		{"/a/b/x", "/a/b/"},
		{"/var/folders/xx/T/go-build-1", "/var/folders/xx/T/"},
		{"/var/folders/xx/T/go-build-1/a/b", "/var/folders/xx/T/"},
		{"/elsewhere/x/y", "/elsewhere/x/"},
		{"rel/base/x", ""},
	} {
		if got := placeOf(c.path, roots, bases, home); got != c.want {
			t.Errorf("placeOf(%q) = %q, want %q", c.path, got, c.want)
		}
	}
	if got := placeOf("/z/y/q", nil, nil, home); got != "/z/y/" {
		t.Errorf("no bases: %q", got)
	}
}

func TestCleanBases(t *testing.T) {
	got := cleanBases([]string{"/a/", "", "rel", "/a", "/b"})
	if want := []string{"/a", "/b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanBases = %v, want %v", got, want)
	}
}

func TestWithResolvedAddsTheOtherSpelling(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	link := filepath.Join(dir, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	in := []string{link, filepath.Join(dir, "missing")}
	got := withResolved(in)
	if want := []string{link, filepath.Join(dir, "missing"), resolved}; !reflect.DeepEqual(got, want) {
		t.Fatalf("withResolved = %v, want %v", got, want)
	}
	if len(in) != 2 {
		t.Fatalf("input changed: %v", in)
	}
}

func TestBothSpellingsOfABaseShareOnePlace(t *testing.T) {
	bases := []string{"/tmp/claude-1000", "/private/tmp/claude-1000"}
	a := placeOf("/private/tmp/claude-1000/x/y", nil, bases, "/home/user")
	b := placeOf("/tmp/claude-1000/z/w", nil, bases, "/home/user")
	if a != "/private/tmp/claude-1000/" || b != "/tmp/claude-1000/" {
		t.Fatalf("places %q %q", a, b)
	}
	// a row spelled with the resolved form, base given only as /tmp plus its resolved form: one place per spelling,
	// and the resolved row lands under the resolved base rather than /private/tmp/claude-1000/x/
	if got := placeOf("/private/tmp/claude-1000/x/y", nil, bases[:1], "/home/user"); got != "/private/tmp/claude-1000/x/" {
		t.Fatalf("without the resolved form: %q", got)
	}
}

func TestSpaceOnAManyRowPlaceSaysSo(t *testing.T) {
	m, _ := keys(scratchModel(), "jjj ")
	if want := "selected 2 rows in " + pScratch + " (enter to see them)"; m.Status != want {
		t.Fatalf("status %q, want %q", m.Status, want)
	}
	one, _ := keys(placesModel(), "jj ") // ~/old/ has no ready row; nvm below has two
	if strings.Contains(one.Status, "selected") {
		t.Fatalf("unexpected status %q", one.Status)
	}
	again, _ := keys(m, " ") // deselect: no message
	if again.Status != "" {
		t.Fatalf("deselect status %q", again.Status)
	}
}

const pScratch = "/private/tmp/claude-1000/"

func scratchRows() []Row {
	rows := placesRows()
	for i := 0; i < 6; i++ {
		rows = append(rows, Row{
			Path:  fmt.Sprintf("/private/tmp/claude-1000/-Users-x-proj%d/s%d", i, i),
			Bytes: -1, Updated: day(2026, 11, 10+i), Source: SrcKept, Guard: "active", Category: "agent", RuleKey: "agent/scratch@1",
		})
	}
	rows = append(rows,
		Row{Path: "/private/tmp/claude-1000/-Users-x-old/s9", Bytes: 4_000_000, Updated: day(2026, 1, 2), Source: SrcAuto, Category: "agent", RuleKey: "agent/scratch@1"},
		Row{Path: "/private/tmp/claude-1000/-Users-y-old/s8", Bytes: 1_000_000, Updated: day(2026, 1, 3), Source: SrcAuto, Category: "agent", RuleKey: "agent/scratch@1"},
	)
	return rows
}

func scratchModel() Model {
	s := placesSummary()
	s.Bases = []string{"/private/tmp/claude-1000"}
	return scanned(scratchRows(), s)
}

func TestScratchRowsShareOnePlace(t *testing.T) {
	m := scratchModel()
	if got, want := placeNames(m), []string{pProj, pNVM, pOld, pScratch, pCmd, pEx}; !reflect.DeepEqual(got, want) {
		t.Fatalf("places = %v, want %v", got, want)
	}
	g, _ := keys(m, "g")
	n := 0
	for _, p := range g.places() {
		if strings.HasPrefix(p.Name, "/private/") {
			n++
			if p.Name != pScratch || p.readyN() != 2 || p.KeptN != 6 || !p.Updated.Equal(day(2026, 11, 15)) {
				t.Errorf("scratch place: %+v", p)
			}
		}
	}
	if n != 1 {
		t.Fatalf("scratch places with g: %d, want 1: %v", n, placeNames(g))
	}
	if len(placeNames(g)) != 7 {
		t.Fatalf("with g: %v", placeNames(g))
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
	// optional-only place: asks first, selects nothing yet
	m, _ = keys(placesModel(), "jj ")
	if len(m.Selected) != 0 || m.PlacePrompt == "" {
		t.Fatalf("optional-only: %v %q", m.Selected, m.PlacePrompt)
	}
	// kept-only place (visible with g)
	m, _ = keys(placesModel(), "g")
	for i, p := range m.places() {
		if p.Name == pKept {
			m.Cursor = i
		}
	}
	m, _ = keys(m, " ")
	if len(m.Selected) != 0 || m.Status != "protected: nothing here can be removed from this screen" {
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
	m, _ := keys(listModel(), "ojj ")
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

func confirmText(m Model) string { return strings.Join(View(m, 100, 30), "\n") }

func TestConfirmSaysWhenRowsAreNotInTheListYouCameFrom(t *testing.T) {
	// places: a search hides the place of one selected row
	m, _ := keys(placesModel(), "a/nvm<enter>d")
	if s := confirmText(m); m.Screen != ScreenConfirm || !strings.Contains(s, "1 of them are not shown in the list you came from.") {
		t.Fatalf("places:\n%s", s)
	}
	// nothing hidden: no line
	m, _ = keys(placesModel(), "ad")
	if strings.Contains(confirmText(m), "not shown") {
		t.Fatalf("a line for rows that are all in view:\n%s", confirmText(m))
	}
	// flat list, a search
	m, _ = keys(listModel(), "a/docker<enter>d")
	if s := confirmText(m); !strings.Contains(s, "4 of them are not shown") {
		t.Fatalf("search:\n%s", s)
	}
	// flat list: o, space, o, d leaves a switched off row selected and hidden
	m, _ = keys(listModel(), "ojj od")
	if s := confirmText(m); !strings.Contains(s, "1 of them are not shown") || !strings.Contains(s, "in categories you switched off") {
		t.Fatalf("o a o d:\n%s", s)
	}
	// space in place A, then place B: the entry of A shows its place, the rows of B do not
	m, _ = keys(placesModel(), " j<enter>ad")
	s := confirmText(m)
	if m.Screen != ScreenConfirm || !strings.Contains(s, "1 of them are not shown") {
		t.Fatalf("place A then B:\n%s", s)
	}
	for _, l := range strings.Split(s, "\n") {
		switch {
		case strings.Contains(l, "proj/node_modules") && !strings.Contains(l, pProj):
			t.Fatalf("an entry outside the open place must show its place: %q", l)
		case strings.Contains(l, ".nvm/versions/node/v") && strings.Contains(l, pProj):
			t.Fatalf("an entry inside the open place must not: %q", l)
		}
	}
}

func manyRows(n int) []Row {
	var rows []Row
	for i := range n {
		rows = append(rows, Row{Path: fmt.Sprintf("/s/p%02d", i), Bytes: 100, Source: SrcAuto, Category: "misc", RuleKey: "misc/x@1"})
	}
	return rows
}

func TestConfirmScrollsAndYConfirmsAtAnyPosition(t *testing.T) {
	m := scanned(manyRows(40), placesSummary())
	m, _ = Update(m, ResizeMsg{Width: 80, Height: 24})
	m, _ = keys(m, "ad")
	if m.Screen != ScreenConfirm || len(m.Confirm) != 40 {
		t.Fatalf("setup: %v %d", m.Screen, len(m.Confirm))
	}
	first := strings.Join(View(m, 80, 24), "\n")
	if !strings.Contains(first, "Delete 40 items") || !strings.Contains(first, "rows 1-21 of 40") {
		t.Fatalf("first page:\n%s", first)
	}
	shown := map[string]int{}
	pages := 0
	for {
		pages++
		for _, l := range confirmPage(m, 80, 24) {
			shown[strings.TrimSpace(l[strings.LastIndex(l, " ")+1:])]++
		}
		if !strings.Contains(strings.Join(View(m, 80, 24), "\n"), "Delete 40 items") {
			t.Fatal("the header must stay on every page")
		}
		next, _ := keys(m, "<pgdn>")
		if next.ConfirmTop == m.ConfirmTop {
			break
		}
		m = next
	}
	if end := strings.Join(View(m, 80, 24), "\n"); !strings.Contains(end, "rows 20-40 of 40") {
		t.Fatalf("last page:\n%s", end)
	}
	for _, p := range m.Confirm {
		if shown[p] == 0 {
			t.Fatalf("%s is not reachable by scrolling", p)
		}
	}
	// j, k and the arrows scroll one entry; y confirms at any position
	up, _ := keys(m, "k<up>")
	down, _ := keys(up, "j<down>")
	if up.ConfirmTop != m.ConfirmTop-2 || down.ConfirmTop != m.ConfirmTop {
		t.Fatalf("top: end %d, after k and up %d, after j and down %d", m.ConfirmTop, up.ConfirmTop, down.ConfirmTop)
	}
	mid, _ := keys(placesAfterScan(manyRows(40)), "ad<pgdn>")
	if mid.ConfirmTop == 0 {
		t.Fatal("did not scroll")
	}
	_, eff := keys(mid, "y")
	if len(eff) != 1 || !reflect.DeepEqual(eff[0].(Delete).Paths, mid.Confirm) || len(mid.Confirm) != 40 {
		t.Fatalf("y while scrolled: %v", eff)
	}
	// below 80x24 the keys stay refused
	small, _ := Update(mid, ResizeMsg{Width: 70, Height: 20})
	if _, eff := keys(small, "y"); len(eff) != 0 {
		t.Fatalf("y in a small window: %v", eff)
	}
}

func placesAfterScan(rows []Row) Model {
	m := scanned(rows, placesSummary())
	m, _ = Update(m, ResizeMsg{Width: 80, Height: 24})
	return m
}

func TestExclusionDuringDeleteKeepsThePlacesCursor(t *testing.T) {
	m, _ := keys(placesModel(), "j dy")
	if m.Screen != ScreenResult || !m.Result.Pending {
		t.Fatalf("setup: %v", m.Screen)
	}
	m, _ = Update(m, ExclusionChangedMsg{Path: "/home/user/proj/node_modules", Excluded: true})
	if m.Cursor >= len(m.places()) || cursorPlaceName(t, m) != pNVM { // the place keeps the cursor, wherever it sorts now
		t.Fatalf("cursor %d of %d places, on %q", m.Cursor, len(m.places()), cursorPlaceName(t, m))
	}
	// the last place, with many flat rows before it: the cursor must stay a place index
	many := manyRows(30)
	m = scanned(append(many, Row{Path: "/zz/last", Bytes: 1, Source: SrcAuto}), placesSummary())
	m, _ = keys(m, "jdy")
	m, _ = Update(m, ExclusionChangedMsg{Path: "/s/p03", Excluded: true})
	if m.Cursor >= len(m.places()) {
		t.Fatalf("cursor %d past the %d places", m.Cursor, len(m.places()))
	}
}

func TestAInAPlaceSelectsReadyRowsOnly(t *testing.T) {
	m, _ := keys(placesModel(), "jj<enter>a") // ~/old/ holds only an optional row
	if len(m.Selected) != 0 || !strings.Contains(m.Status, "no safe or recommended rows") {
		t.Fatalf("optional-only place: %v %q", m.Selected, m.Status)
	}
	rows := []Row{
		{Path: "/home/user/mix/a", Bytes: 10, Source: SrcAuto},
		{Path: "/home/user/mix/b", Bytes: 20, Source: SrcOptionalOn, Label: core.LabelRecommended},
		{Path: "/home/user/mix/c", Bytes: 30, Source: SrcOptionalOff},
		{Path: "/home/user/mix/d", Bytes: 40, Source: SrcOptionalOn, Label: core.LabelYourCall},
	}
	mix := scanned(rows, placesSummary())
	mix, _ = keys(mix, "<enter>a")
	if !reflect.DeepEqual(mix.Selected, map[string]bool{"/home/user/mix/a": true, "/home/user/mix/b": true}) {
		t.Fatalf("selected %v", mix.Selected)
	}
}

func TestStatusLinesSayOneRowAndTheirOwnSort(t *testing.T) {
	m, _ := keys(placesModel(), "jj<enter>") // ~/old/ has one row
	if s := screen(m); !strings.Contains(s, "1 row, sorted by") || strings.Contains(s, "1 rows") {
		t.Fatalf("place status:\n%s", s)
	}
	p := placesModel()
	p.Sort = SortAge
	p, _ = keys(p, "/zzz<enter>")
	if s := screen(p); !strings.Contains(s, "0 places, sorted by size") || strings.Contains(s, "age, oldest") {
		t.Fatalf("places status:\n%s", s)
	}
}

func emptyPlacesModel() Model {
	rows := []Row{{Path: "/home/user/work/repo/.venv", Bytes: -1, Source: SrcKept, Guard: "dirty", Category: "python", RuleKey: "python/venv@1"}}
	return scanned(rows, placesSummary())
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
		{"places_scratch", func() Model { m, _ := keys(scratchModel(), "g"); return m }},
		{"place_scratch_open", func() Model { m, _ := keys(scratchModel(), "gjjj<enter>"); return m }},
	}...)
}
