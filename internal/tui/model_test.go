package tui

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"
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
			Source: SrcOptionalOn, Category: "node", RuleKey: "node/node-modules@1", Why: "dependencies of a project idle for 100 days"},
		{Path: "/home/user/old/node_modules", Bytes: 800_000_000, Created: day(2024, 5, 1), Updated: day(2025, 2, 3),
			Source: SrcOptionalOff, Category: "node", RuleKey: "node/node-modules@1", Why: "dependencies of a project idle for 400 days"},
		{Path: "/home/user/.npm/_cacache", Bytes: 500_000_000, Created: day(2025, 8, 8), Updated: day(2026, 11, 20),
			Source: SrcAuto, Category: "node", RuleKey: "node/npm-cache@1", Block: WholesaleReason("npm cache clean")},
		{Path: "/home/user/proj2/target", Bytes: 700_000_000, Created: day(2026, 1, 1), Updated: day(2026, 9, 9),
			Source: SrcExcluded, Base: SrcAuto, Category: "rust", RuleKey: "rust/target@1"},
		{Path: "/home/user/work/repo/.venv", Bytes: 90_000_000, Created: day(2026, 2, 2), Updated: day(2026, 11, 20),
			Source: SrcKept, Category: "python", RuleKey: "python/venv@1", Block: "kept by a guard: git repository with uncommitted work"},
		{Path: "/home/user/containers/stopped", Bytes: 60_000_000, Created: day(2026, 10, 1), Updated: day(2026, 10, 2),
			Source: SrcOptionalOn, Category: "docker", RuleKey: "docker/stopped@1", Containers: []string{"abc123", "def456"}},
		{Path: "/home/user/a/very/long/directory/name/that/does/not/fit/in/the/path/column/of/the/list/target", Bytes: 20_000_000,
			Source: SrcAuto, Category: "rust", RuleKey: "rust/target@1"},
		{Path: "/home/user/zz", Bytes: 10_000_000, Updated: day(2026, 11, 10), Source: SrcAuto, Category: "misc", RuleKey: "misc/zz@1"},
	}
}

func fixtureSummary() SummaryData {
	return SummaryData{Now: now, Agents: 2, Interval: 30 * time.Minute, Next: day(2026, 11, 24).Add(14*time.Hour + 30*time.Minute),
		FreeNow: 40_000_000_000}
}

func listModel() Model {
	m := NewModel(true)
	m, _ = Update(m, ScanDoneMsg{Rows: fixtureRows(), Summary: fixtureSummary()})
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
			case "esc", "enter", "up", "down":
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

func TestInitStartsScanAndEscCancels(t *testing.T) {
	if got := Init(); !reflect.DeepEqual(got, []Effect{StartScan{}}) {
		t.Fatalf("Init = %v", got)
	}
	m, eff := keys(NewModel(true), "<esc>")
	if !reflect.DeepEqual(eff, []Effect{CancelScan{}}) || m.Screen != ScreenList {
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
		if m.Selected[r.Path] != r.selectable() {
			t.Fatalf("a: %s selected=%v selectable=%v", r.Path, m.Selected[r.Path], r.selectable())
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
	if !m.Selected["/home/user/old/node_modules"] {
		t.Fatal("a with o on selects the optional-off row")
	}
	m, _ = keys(m, "o")
	if m.Selected["/home/user/old/node_modules"] {
		t.Fatal("hiding the group must drop its selection")
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
	if m.Filter != "" || len(m.visible()) != 8 {
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

func TestSelectedHiddenByFilterIsNotDeleted(t *testing.T) {
	m, _ := keys(listModel(), "a/docker<enter>d")
	if m.Screen != ScreenConfirm || !reflect.DeepEqual(m.Confirm, []string{"/home/user/containers/stopped"}) {
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
	if m.Screen != ScreenList || len(eff) != 0 {
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

func TestPropertyNeverSelectsDimAndDeleteEqualsVisibleSelection(t *testing.T) {
	alphabet := []string{"j", "k", " ", "a", "x", "s", "o", "/", "d", "y", "n", "q", "e", "<esc>", "<enter>", "<up>", "<down>", "<bs>"}
	rng := rand.New(rand.NewSource(7))
	for seq := 0; seq < 300; seq++ {
		m := listModel()
		for step := 0; step < 80; step++ {
			prev := m
			k := alphabet[rng.Intn(len(alphabet))]
			var eff []Effect
			m, eff = keys(m, k)
			for _, e := range eff {
				switch e := e.(type) {
				case AddExclusion:
					m, _ = Update(m, ExclusionChangedMsg{Path: e.Path, Excluded: true})
				case RemoveExclusion:
					m, _ = Update(m, ExclusionChangedMsg{Path: e.Path, Excluded: false})
				case Delete:
					want := pathsOf(prev.selected())
					if !reflect.DeepEqual(e.Paths, want) || !reflect.DeepEqual(prev.Confirm, want) {
						t.Fatalf("seq %d step %d: Delete %v, visible selected %v", seq, step, e.Paths, want)
					}
					m, _ = Update(m, DeleteDoneMsg{})
				}
			}
			byPath := map[string]Row{}
			for _, r := range m.Rows {
				byPath[r.Path] = r
			}
			for p := range m.Selected {
				if !byPath[p].selectable() {
					t.Fatalf("seq %d step %d: dim row %s is selected", seq, step, p)
				}
			}
		}
	}
}
