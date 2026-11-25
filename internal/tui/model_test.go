package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
)

var now = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func day(y int, mo time.Month, d int) time.Time { return time.Date(y, mo, d, 0, 0, 0, 0, time.UTC) }

func fixtureRows() []Row {
	return []Row{
		{Path: "/home/user/.cache/go-build", Bytes: 3_100_000_000, Created: day(2025, 1, 10), Updated: day(2026, 11, 1),
			Source: SrcAuto, Category: "go", RuleKey: "go/go-build@1", Why: "Go build cache not used for 20 days",
			Guards:   []string{"deny: not on the deny list", "fresh: newest change 23 days ago, minimum 14"},
			Children: []Child{{"3a", 900_000_000}, {"7f", 400_000_000}}},
		{Path: "/home/user/proj/node_modules", Bytes: 1_200_000_000, Created: day(2026, 3, 2), Updated: day(2026, 6, 30),
			Source: SrcOptionalOn, Label: core.LabelRecommended, Category: "node", RuleKey: "node/node-modules@1", Why: "dependencies of a project idle for 100 days"},
		{Path: "/home/user/old/node_modules", Bytes: 800_000_000, Created: day(2024, 5, 1), Updated: day(2025, 2, 3),
			Source: SrcOptionalOff, Category: "node", RuleKey: "node/node-modules@1", Why: "dependencies of a project idle for 400 days"},
		{Path: "/home/user/.npm/_cacache", Bytes: 500_000_000, Created: day(2025, 8, 8), Updated: day(2026, 11, 20),
			Source: SrcAuto, Category: "node", RuleKey: "node/npm-cache@1", Block: WholesaleReason("npm cache clean")},
		{Path: "/home/user/proj2/target", Bytes: 700_000_000, Created: day(2026, 1, 1), Updated: day(2026, 9, 9),
			Source: SrcExcluded, Base: SrcAuto, Category: "rust", RuleKey: "rust/target@1"},
		{Path: "/home/user/work/repo/.venv", Bytes: 90_000_000, Created: day(2026, 2, 2), Updated: day(2026, 11, 20),
			Source: SrcKept, Guard: "dirty", Category: "python", RuleKey: "python/venv@1", Block: "kept by a guard: git repository with uncommitted work"},
		{Path: "/home/user/containers/stopped", Bytes: 60_000_000, Created: day(2026, 10, 1), Updated: day(2026, 10, 2),
			Source: SrcOptionalOn, Category: "docker", RuleKey: "docker/stopped@1", Members: []string{"docker:abc123", "docker:def456"}},
		{Path: "/home/user/a/very/long/directory/name/that/does/not/fit/in/the/path/column/of/the/list/target", Bytes: 20_000_000,
			Source: SrcAuto, Category: "rust", RuleKey: "rust/target@1"},
		{Path: "/home/user/zz", Bytes: 10_000_000, Updated: day(2026, 11, 10), Source: SrcAuto, Category: "misc", RuleKey: "misc/zz@1"},
	}
}

func fixtureSummary() SummaryData {
	return SummaryData{Now: now, Agents: 2, Interval: 30 * time.Minute, Next: day(2026, 11, 24).Add(14*time.Hour + 30*time.Minute),
		FreeNow: 40_000_000_000}
}

// scanned is a model right after its scan with the start selection cleared, so that a test of the keys starts from
// nothing selected. The start selection itself is tested in tl1_test.go and in the property test.
func scanned(rows []Row, s SummaryData) Model {
	m, _ := Update(NewModel(true), ScanDoneMsg{Rows: rows, Summary: s})
	m.Selected = map[string]bool{}
	return m
}

func listModel() Model { return flat(scanned(fixtureRows(), fixtureSummary())) }

// flat switches a model that just got its scan from the places to the flat candidate list (`c`).
func flat(m Model) Model {
	m, _ = Update(m, KeyMsg{Rune: 'c'})
	return m
}

// keys feeds a script to Update. Plain characters are runes; <esc> <enter> <up> <down> <bs> <c-c> are named keys.
func keys(m Model, script string) (Model, []Effect) {
	var all []Effect
	for script != "" {
		var k KeyMsg
		if strings.HasPrefix(script, "<") {
			end := strings.Index(script, ">")
			name := script[1:end]
			script = script[end+1:]
			switch name {
			case "esc", "enter", "up", "down", "pgup", "pgdn":
				k.Special = name
			case "bs":
				k.Special = KeyBackspace
			case "c-c":
				k = KeyMsg{Rune: 'c', Ctrl: true}
			}
		} else {
			r := []rune(script)
			k.Rune = r[0]
			script = string(r[1:])
		}
		var eff []Effect
		m, eff = Update(m, k)
		all = append(all, eff...)
	}
	return m, all
}

func pathsOf(rows []Row) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Path)
	}
	return out
}

// fx4Rows has every kind of row the default view must sort out.
func fx4Rows() []Row {
	return []Row{
		{Path: "/a/auto", Bytes: 100_000_000, Source: SrcAuto, Category: "go", RuleKey: "go/a@1"},
		{Path: "/a/opt-on", Bytes: 90_000_000, Source: SrcOptionalOn, Label: core.LabelRecommended, Category: "node", RuleKey: "node/a@1"},
		{Path: "/a/opt-off", Bytes: 50_000_000, Source: SrcOptionalOff, Category: "node", RuleKey: "node/a@1"},
		{Path: "/a/opt-off-unknown", Bytes: -1, Source: SrcOptionalOff, Category: "node", RuleKey: "node/a@1"},
		{Path: "/a/excluded", Bytes: 80_000_000, Source: SrcExcluded, Base: SrcAuto, Category: "go", RuleKey: "go/a@1"},
		{Path: "/a/kept-dirty", Bytes: -1, Source: SrcKept, Guard: "dirty", Category: "git", RuleKey: "git/w@1"},
		{Path: "/a/kept-odd", Bytes: -1, Source: SrcKept, Guard: "some-odd-guard-token-name", Category: "git", RuleKey: "git/w@1"},
	}
}

func fx4Model(rows []Row) Model {
	return flat(scanned(rows, fixtureSummary()))
}

func screen(m Model) string { return strings.Join(View(m, 100, 30), "\n") }

func TestDefaultViewHoldsCandidatesAndExcludedOnly(t *testing.T) {
	m := fx4Model(fx4Rows())
	want := []string{"/a/auto", "/a/opt-on", "/a/excluded"}
	if got := pathsOf(m.visible()); !reflect.DeepEqual(got, want) {
		t.Fatalf("default view = %v, want %v", got, want)
	}
	g, _ := keys(m, "g")
	if got := pathsOf(g.visible()); len(got) != 5 || got[3] != "/a/kept-dirty" {
		t.Fatalf("g view = %v", got)
	}
	for _, r := range g.visible() {
		if r.Source == SrcKept && r.selectable() {
			t.Fatalf("kept row %s is selectable", r.Path)
		}
	}
	o, _ := keys(m, "o")
	if got := pathsOf(o.visible()); len(got) != 5 || got[3] != "/a/opt-off" {
		t.Fatalf("o view = %v", got)
	}
	both, _ := keys(m, "og")
	if len(both.visible()) != 7 {
		t.Fatalf("o and g view = %v", pathsOf(both.visible()))
	}
	back, _ := keys(both, "g")
	if len(back.visible()) != 5 {
		t.Fatalf("second g must hide kept rows: %v", pathsOf(back.visible()))
	}
}

func TestJAndKStillMoveTheCursor(t *testing.T) {
	m := fx4Model(fx4Rows())
	m, _ = keys(m, "jj")
	if m.Cursor != 2 {
		t.Fatalf("j: cursor %d", m.Cursor)
	}
	if m, _ = keys(m, "k"); m.Cursor != 1 || m.ShowKept {
		t.Fatalf("k must move up and not toggle kept: cursor %d, kept %v", m.Cursor, m.ShowKept)
	}
}

func TestExcludedRowVisibleAndUnexcludeUnchanged(t *testing.T) {
	m := fx4Model(fx4Rows())
	for i, r := range m.visible() {
		if r.Path == "/a/excluded" {
			m.Cursor = i
		}
	}
	_, eff := keys(m, "x")
	if !reflect.DeepEqual(eff, []Effect{RemoveExclusion{Path: "/a/excluded"}}) {
		t.Fatalf("x on an excluded row: %v", eff)
	}
	m, _ = Update(m, ExclusionChangedMsg{Path: "/a/excluded", Excluded: false})
	for _, r := range m.Rows {
		if r.Path == "/a/excluded" && r.Source != SrcAuto {
			t.Fatalf("un-excluded row did not return to auto: %v", r.Source)
		}
	}
}

func TestSearchFindsKeptRowsOnlyWhileGIsOn(t *testing.T) {
	m := fx4Model(fx4Rows())
	if got, _ := keys(m, "/kept<enter>"); len(got.visible()) != 0 {
		t.Fatalf("kept rows found with g off: %v", pathsOf(got.visible()))
	}
	if got, _ := keys(m, "g/kept<enter>"); len(got.visible()) != 2 {
		t.Fatalf("kept rows not found with g on: %v", pathsOf(got.visible()))
	}
}

func TestHiddenHintLines(t *testing.T) {
	s := screen(fx4Model(fx4Rows()))
	for _, want := range []string{"switched off: 2 | " + fmtBytes(50_000_000) + "+ | o", "protected: 2 | g"} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary lacks %q:\n%s", want, s)
		}
	}
	if s := strings.Join(View(fx4Model(fx4Rows()), 80, 24), "\n"); !strings.Contains(s, "switched off: 2 | "+fmtBytes(50_000_000)+"+ | o  ") || !strings.Contains(s, "protected: 2 | g  ") {
		t.Fatalf("hint lines are cut at 80 columns:\n%s", s)
	}
	known := fx4Rows()
	known[3].Bytes = 10_000_000
	if s := screen(fx4Model(known)); !strings.Contains(s, "switched off: 2 | "+fmtBytes(60_000_000)+" | o") {
		t.Fatalf("known sizes:\n%s", s)
	}
	m, _ := keys(fx4Model(fx4Rows()), "og")
	if s := screen(m); strings.Contains(s, "switched off: ") || strings.Contains(s, "protected: 2") {
		t.Fatalf("nothing is hidden, no hint expected:\n%s", s)
	}
	if s := screen(fx4Model(fx4Rows()[:1])); strings.Contains(s, "switched off: ") || strings.Contains(s, "protected: 1") {
		t.Fatalf("no hidden rows, no hint expected:\n%s", s)
	}
}

func TestEmptyDefaultListSaysNothingIsReady(t *testing.T) {
	r := fx4Rows()
	s := screen(fx4Model([]Row{r[2], r[5]}))
	for _, want := range []string{"nothing to remove here", "switched off: 1 |", "protected: 1 | g"} {
		if !strings.Contains(s, want) {
			t.Fatalf("empty list lacks %q:\n%s", want, s)
		}
	}
}

func TestKeptRowSaysWhy(t *testing.T) {
	for _, tok := range []string{"young", "open", "process", "secret", "tracked", "not-ignored", "no-marker", "nested-git", "place",
		"deny", "dirty", "untracked", "stash", "unpushed", "gitfail", "orphan", "active", "error", "budget", "in use", "locked", "paused", "not-a-candidate", "refresh"} {
		r := Row{Path: "/k", Bytes: -1, Source: SrcKept, Guard: tok}
		word := guardReason(tok)
		if _, known := guardWords[tok]; !known || word == "" {
			t.Fatalf("no plain words for %q", tok)
		}
		if got := labelText(r, false); got != "protected · "+word || cols(got) > labelW {
			t.Fatalf("label of %q = %q", tok, got)
		}
		if got := labelText(r, true); got != "protected-"+word || cols(got) > labelW {
			t.Fatalf("plain label of %q = %q", tok, got)
		}
		if guardMeaning(tok) == "" {
			t.Fatalf("no meaning for %q", tok)
		}
		m := fx4Model([]Row{r})
		m, _ = keys(m, "g<enter>")
		if s := screen(m); !strings.Contains(s, "protected-"+word) || !strings.Contains(s, guardMeaning(tok)) {
			t.Fatalf("detail of %q:\n%s", tok, s)
		}
	}
	odd := Row{Path: "/k", Source: SrcKept, Guard: "some-odd-guard-token-name"}
	if got := labelText(odd, true); !strings.HasPrefix(got, "protected-some") || !strings.HasSuffix(got, "...") || cols(got) > labelW {
		t.Fatalf("unknown token label = %q", got)
	}
	if got := labelText(odd, false); !strings.HasSuffix(got, "...") || cols(got) > labelW {
		t.Fatalf("unknown token label = %q", got)
	}
	if guardMeaning("some-odd-guard-token-name") == "" || labelText(Row{Source: SrcKept}, true) != "protected" {
		t.Fatal("unknown or missing token must not panic or go empty")
	}
	m, _ := keys(fx4Model(fx4Rows()), "g")
	if s := screen(m); !strings.Contains(s, "protected-dirty") || !strings.Contains(s, "? /a/kept-dirty") {
		t.Fatalf("kept rows in the g view:\n%s", s)
	}
}

func cursorPathOf(t *testing.T, m Model) string {
	t.Helper()
	r, ok := m.cursorRow()
	if !ok {
		t.Fatalf("cursor %d is outside the %d visible rows", m.Cursor, len(m.visible()))
	}
	return r.Path
}

func TestUnexcludeOfUnknownOriginStaysWhereItIs(t *testing.T) {
	rows := []Row{
		{Path: "/a", Bytes: 300, Source: SrcAuto},
		{Path: "/x", Bytes: 250, Source: SrcExcluded}, // no Base, no Origin: the scan gave no source
		{Path: "/b", Bytes: 200, Source: SrcAuto},
		{Path: "/c", Bytes: 100, Source: SrcAuto},
	}
	m := fx4Model(rows)
	m, _ = keys(m, "j") // /a is first selectable; /x is next
	if cursorPathOf(t, m) != "/x" {
		t.Fatalf("cursor on %q", cursorPathOf(t, m))
	}
	m, eff := keys(m, "x")
	if !reflect.DeepEqual(eff, []Effect{RemoveExclusion{Path: "/x"}}) {
		t.Fatalf("first x: %v", eff)
	}
	m, _ = Update(m, ExclusionChangedMsg{Path: "/x", Excluded: false})
	if got := pathsOf(m.visible()); !reflect.DeepEqual(got, []string{"/a", "/x", "/b", "/c"}) || cursorPathOf(t, m) != "/x" {
		t.Fatalf("row left the list or the cursor moved: %v cursor %d", got, m.Cursor)
	}
	m, eff = keys(m, "x")
	if len(eff) != 0 || cursorPathOf(t, m) != "/x" || m.Status == "" {
		t.Fatalf("second x must hit the same row and exclude nothing: %v %q on %q", eff, m.Status, cursorPathOf(t, m))
	}
	for _, r := range m.Rows {
		if r.Source == SrcExcluded {
			t.Fatalf("%s got excluded", r.Path)
		}
	}
	d, _ := keys(m, "<enter>")
	if s := screen(d); !strings.Contains(s, "protected-refresh") {
		t.Fatalf("detail:\n%s", s)
	}
}

func TestUnexcludeOfOptionalOffRowStaysUntilTheCursorLeaves(t *testing.T) {
	rows := []Row{
		{Path: "/a", Bytes: 300, Source: SrcAuto},
		{Path: "/o", Bytes: 200, Source: SrcExcluded, Base: SrcOptionalOff},
		{Path: "/b", Bytes: 100, Source: SrcAuto},
	}
	m := fx4Model(rows)
	m, _ = keys(m, "j")
	if cursorPathOf(t, m) != "/o" {
		t.Fatalf("cursor on %q", cursorPathOf(t, m))
	}
	m, _ = Update(m, ExclusionChangedMsg{Path: "/o", Excluded: false})
	if got := pathsOf(m.visible()); len(got) != 3 || cursorPathOf(t, m) != "/o" {
		t.Fatalf("row left the list: %v cursor %d", got, m.Cursor)
	}
	if _, eff := keys(m, "x"); !reflect.DeepEqual(eff, []Effect{AddExclusion{Path: "/o"}}) {
		t.Fatalf("x must hit the same row: %v", eff)
	}
	m, _ = keys(m, "j")
	if got := pathsOf(m.visible()); !reflect.DeepEqual(got, []string{"/a", "/b"}) || cursorPathOf(t, m) != "/b" {
		t.Fatalf("after leaving: %v on %q", got, cursorPathOf(t, m))
	}
}

func TestTogglesAndEscKeepTheCursorOnTheSameRow(t *testing.T) {
	rows := []Row{
		{Path: "/b", Bytes: 20, Source: SrcAuto},
		{Path: "/c", Bytes: 10, Source: SrcAuto},
		{Path: "/a", Bytes: -1, Source: SrcKept, Guard: "dirty"},
		{Path: "/a0", Bytes: 5, Source: SrcOptionalOff},
	}
	m := fx4Model(rows)
	m, _ = keys(m, "sssj") // sort by path; cursor on /c
	for _, key := range []string{"g", "g", "o", "o", "go", "og"} {
		if got, _ := keys(m, key); cursorPathOf(t, got) != "/c" {
			t.Fatalf("%q moved the cursor to %q", key, cursorPathOf(t, got))
		}
	}
	m, _ = keys(m, "gk") // /b above /c, and /a above them
	m, _ = keys(m, "<esc>")
	if cursorPathOf(t, m) != "/b" {
		t.Fatalf("esc: cursor on %q, want the first selectable row", cursorPathOf(t, m))
	}
}

func TestOptionalHintIgnoresRowsThatStayBlocked(t *testing.T) {
	rows := []Row{
		{Path: "/a", Bytes: 1, Source: SrcAuto},
		{Path: "docker prune", Bytes: 99, Source: SrcOptionalOff, Block: "never from this screen"},
	}
	if s := screen(fx4Model(rows)); strings.Contains(s, "switched off: ") {
		t.Fatalf("hint counts a row that o cannot make selectable:\n%s", s)
	}
}

func TestCursorStartsOnFirstSelectableRow(t *testing.T) {
	rows := []Row{
		{Path: "/a/excluded", Bytes: 900, Source: SrcExcluded, Base: SrcAuto},
		{Path: "/a/blocked", Bytes: 800, Source: SrcAuto, Block: "cleaned by x"},
		{Path: "/a/auto", Bytes: 100, Source: SrcAuto},
	}
	m := fx4Model(rows)
	if r, _ := m.cursorRow(); r.Path != "/a/auto" {
		t.Fatalf("cursor on %q", r.Path)
	}
}

func TestSelectedCountIsGlobal(t *testing.T) {
	m, _ := keys(fx4Model(fx4Rows()), "a")
	if s := screen(m); !strings.Contains(s, "2 of 4 selected") { // 4 selectable rows in the scan, 2 in view
		t.Fatalf("counts:\n%s", s)
	}
}

func TestInitStartsScanAndEscCancels(t *testing.T) {
	if got := Init(); !reflect.DeepEqual(got, []Effect{StartScan{}}) {
		t.Fatalf("Init = %v", got)
	}
	m, eff := keys(NewModel(true), "<esc>")
	if !reflect.DeepEqual(eff, []Effect{CancelScan{}}) || m.Screen != ScreenPlaces {
		t.Fatalf("esc while scanning: %v %v", eff, m.Screen)
	}
	if _, eff = keys(NewModel(true), "q"); len(eff) != 0 {
		t.Fatalf("q must not quit while scanning: %v", eff)
	}
}

func TestSelectDeselectAndSelectAllSkipsDim(t *testing.T) {
	m := listModel()
	first := m.visible()[0].Path
	m, _ = keys(m, " ")
	if !m.Selected[first] {
		t.Fatal("space did not select")
	}
	m, _ = keys(m, " ")
	if m.Selected[first] {
		t.Fatal("second space did not deselect")
	}
	m, _ = keys(m, "a")
	for _, r := range m.visible() {
		if m.Selected[r.Path] != isReady(r) {
			t.Fatalf("a: %s selected=%v safe or recommended=%v", r.Path, m.Selected[r.Path], isReady(r))
		}
	}
	if m.Selected["/home/user/old/node_modules"] {
		t.Fatal("optional-off row is not in view and must not be selected")
	}
}

func TestSpaceOnDimRowDoesNothing(t *testing.T) {
	m := listModel()
	for i, r := range m.visible() {
		if r.selectable() {
			continue
		}
		m.Cursor = i
		n, _ := keys(m, " ")
		if len(n.Selected) != 0 {
			t.Fatalf("space selected dim row %s", r.Path)
		}
	}
}

func TestOptionalOffGroupToggle(t *testing.T) {
	m := listModel()
	for _, r := range m.visible() {
		if r.Source == SrcOptionalOff {
			t.Fatal("optional-off row listed before o")
		}
	}
	m, _ = keys(m, "o")
	var found bool
	for _, r := range m.visible() {
		if r.Source == SrcOptionalOff {
			found = true
		}
	}
	if !found || len(m.Selected) != 0 {
		t.Fatalf("o: found=%v preselected=%v", found, m.Selected)
	}
	m, _ = keys(m, "a")
	if m.Selected["/home/user/old/node_modules"] {
		t.Fatal("a must not select a row of a switched off category")
	}
	m, _ = keys(m, "jj ") // the third row by size
	if !m.Selected["/home/user/old/node_modules"] {
		t.Fatal("space selects the switched off row")
	}
	m, _ = keys(m, "o")
	if !m.Selected["/home/user/old/node_modules"] {
		t.Fatal("hiding optional rows with o must keep them selected (the selection is global)")
	}
}

func TestExcludeEmitsEffectAndRowTurnsDim(t *testing.T) {
	m := listModel()
	target := m.visible()[0].Path
	m, eff := keys(m, " x")
	if !reflect.DeepEqual(eff, []Effect{AddExclusion{Path: target}}) {
		t.Fatalf("effects = %v", eff)
	}
	if !m.Selected[target] || m.visible()[0].Source == SrcExcluded {
		t.Fatal("the model must wait for ExclusionChangedMsg")
	}
	m, _ = Update(m, ExclusionChangedMsg{Path: target, Excluded: true})
	if m.visible()[0].selectable() || m.Selected[target] {
		t.Fatal("row must be dim and deselected after the exclusion")
	}
	m, eff = keys(m, "x")
	if !reflect.DeepEqual(eff, []Effect{RemoveExclusion{Path: target}}) {
		t.Fatalf("effects = %v", eff)
	}
	m, _ = Update(m, ExclusionChangedMsg{Path: target, Excluded: false})
	if !m.visible()[0].selectable() || m.visible()[0].Source != SrcAuto {
		t.Fatalf("row did not come back: %+v", m.visible()[0])
	}
}

func TestDeleteConfirmsExactlySelectedPathsInOrder(t *testing.T) {
	m := listModel()
	m, _ = keys(m, " <down> ")
	want := []string{m.visible()[0].Path, m.visible()[1].Path}
	m, eff := keys(m, "d")
	if m.Screen != ScreenConfirm || len(eff) != 0 {
		t.Fatalf("d: screen=%v eff=%v", m.Screen, eff)
	}
	_, eff = keys(m, "y")
	if !reflect.DeepEqual(eff, []Effect{Delete{Paths: want}}) {
		t.Fatalf("y: %v, want %v", eff, want)
	}
	_, eff = keys(m, "<enter>")
	if !reflect.DeepEqual(eff, []Effect{Delete{Paths: want}}) {
		t.Fatalf("enter: %v", eff)
	}
}

func TestDeleteCancelEmitsNothing(t *testing.T) {
	for _, cancel := range []string{"n", "<esc>", "q"} {
		m := listModel()
		m, eff := keys(m, " d"+cancel)
		if len(eff) != 0 || m.Screen != ScreenList {
			t.Fatalf("cancel %q: screen=%v eff=%v", cancel, m.Screen, eff)
		}
	}
}

func TestDWithoutSelectionDoesNothing(t *testing.T) {
	m, eff := keys(listModel(), "d")
	if m.Screen != ScreenList || len(eff) != 0 || m.Status == "" {
		t.Fatalf("screen=%v eff=%v status=%q", m.Screen, eff, m.Status)
	}
}

func TestQuitOnlyFromList(t *testing.T) {
	_, eff := keys(listModel(), "q")
	if !reflect.DeepEqual(eff, []Effect{Quit{}}) {
		t.Fatalf("list q: %v", eff)
	}
	for _, script := range []string{"<enter>q", " dq"} {
		m, eff := keys(listModel(), script)
		if len(eff) != 0 || m.Screen != ScreenList {
			t.Fatalf("%q: screen=%v eff=%v", script, m.Screen, eff)
		}
	}
	_, eff = keys(listModel(), "<c-c>")
	if !reflect.DeepEqual(eff, []Effect{Quit{}}) {
		t.Fatalf("ctrl-c: %v", eff)
	}
}

func TestSortCycle(t *testing.T) {
	m := listModel()
	m, _ = keys(m, "o")
	want := map[SortKey][]string{
		SortSize:    {"/home/user/.cache/go-build", "/home/user/proj/node_modules", "/home/user/old/node_modules"},
		SortAge:     {"/home/user/old/node_modules", "/home/user/.cache/go-build", "/home/user/.npm/_cacache"},
		SortUpdated: {"/home/user/old/node_modules", "/home/user/proj/node_modules", "/home/user/proj2/target"},
	}
	for _, k := range []SortKey{SortSize, SortAge, SortUpdated} {
		if m.Sort != k {
			t.Fatalf("sort = %v, want %v", m.Sort, k)
		}
		if got := pathsOf(m.visible())[:3]; !reflect.DeepEqual(got, want[k]) {
			t.Fatalf("sort %v: %v", k, got)
		}
		m, _ = keys(m, "s")
	}
	if m.Sort != SortPath {
		t.Fatalf("sort = %v", m.Sort)
	}
	got := pathsOf(m.visible())
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("not sorted by path: %v", got)
		}
	}
	m, _ = keys(m, "s")
	if m.Sort != SortSize {
		t.Fatal("cycle did not wrap")
	}
}

func TestUnknownAgeSortsLast(t *testing.T) {
	m, _ := keys(listModel(), "s")
	v := m.visible()
	last := v[len(v)-1]
	if !last.Created.IsZero() {
		t.Fatalf("a row with a known age is last: %s", last.Path)
	}
	if got := fmtAge(time.Time{}, now); got != "-" {
		t.Fatalf("unknown age = %q", got)
	}
}

func TestFilter(t *testing.T) {
	m, _ := keys(listModel(), "/NODE")
	if !m.Filtering || len(m.visible()) != 2 {
		t.Fatalf("filtering=%v visible=%v", m.Filtering, pathsOf(m.visible()))
	}
	m, _ = keys(m, "<bs><bs><bs><bs><bs>docker")
	if got := pathsOf(m.visible()); !reflect.DeepEqual(got, []string{"/home/user/containers/stopped"}) {
		t.Fatalf("category filter: %v", got)
	}
	m, _ = keys(m, "<enter>j")
	if m.Filtering {
		t.Fatal("enter must end editing")
	}
	m, _ = keys(m, "<esc>")
	if m.Filter != "" || len(m.visible()) != 7 {
		t.Fatalf("esc did not clear the filter: %q %d", m.Filter, len(m.visible()))
	}
	m, _ = keys(m, "/excluded")
	if got := pathsOf(m.visible()); !reflect.DeepEqual(got, []string{"/home/user/proj2/target"}) {
		t.Fatalf("source filter: %v", got)
	}
	m, _ = keys(m, "<esc>")
	if m.Filter != "" || m.Filtering {
		t.Fatal("esc while editing must clear and stop")
	}
}

func TestSelectedHiddenByFilterStaysInTheDeleteList(t *testing.T) {
	m, _ := keys(listModel(), "a/docker<enter>d")
	want := []string{"/home/user/.cache/go-build", "/home/user/proj/node_modules",
		"/home/user/a/very/long/directory/name/that/does/not/fit/in/the/path/column/of/the/list/target", "/home/user/zz"}
	if m.Screen != ScreenConfirm || !reflect.DeepEqual(m.Confirm, want) {
		t.Fatalf("confirm = %v", m.Confirm)
	}
}

func TestDetailOpensAndBackReturns(t *testing.T) {
	m, _ := keys(listModel(), "<enter>")
	if m.Screen != ScreenDetail {
		t.Fatal("enter did not open the detail")
	}
	m, _ = keys(m, " ")
	if len(m.Selected) != 1 {
		t.Fatal("space works in the detail")
	}
	m, _ = keys(m, "<esc>")
	if m.Screen != ScreenList {
		t.Fatal("esc did not go back")
	}
}

func TestPausedBlocksDelete(t *testing.T) {
	m := NewModel(true)
	s := fixtureSummary()
	s.Paused = true
	m, _ = Update(m, ScanDoneMsg{Rows: fixtureRows(), Summary: s})
	m, eff := keys(m, "ad")
	if m.Screen != ScreenPlaces || len(eff) != 0 {
		t.Fatalf("screen=%v eff=%v", m.Screen, eff)
	}
	if got := View(m, 80, 24); !strings.Contains(strings.Join(got, "\n"), "paused: run sapulidi resume") {
		t.Fatal("paused message missing")
	}
}

func TestDeleteDoneShowsResultAndDropsRows(t *testing.T) {
	m, _ := keys(listModel(), " dy")
	if !m.Result.Pending {
		t.Fatal("result must be pending until DeleteDoneMsg")
	}
	gone := m.Confirm[0]
	m, _ = Update(m, DeleteDoneMsg{Result: Result{Freed: 5, Removed: []string{gone}, Kept: []KeptItem{{"/x", "open"}}}})
	if m.Screen != ScreenResult || m.Result.Pending || len(m.Selected) != 0 {
		t.Fatalf("screen=%v result=%+v", m.Screen, m.Result)
	}
	for _, r := range m.Rows {
		if r.Path == gone {
			t.Fatal("removed row is still listed")
		}
	}
	m, _ = keys(m, "q")
	if m.Screen != ScreenList {
		t.Fatal("q in the result closes it")
	}
}

func TestUpdateDoesNotMutateItsInput(t *testing.T) {
	m := listModel()
	before := listModel()
	for _, script := range []string{" ", "a", "o", "s", "/x", "<enter>"} {
		keys(m, script)
	}
	Update(m, ExclusionChangedMsg{Path: m.Rows[0].Path, Excluded: true})
	if !reflect.DeepEqual(m, before) {
		t.Fatal("Update changed its input")
	}
}
