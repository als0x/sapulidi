package tui

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// checkLine fails when a line holds a control character or is wider than w terminal columns. Nothing is stripped
// from a plain line; a coloured line may hold only well-formed SGR colour codes, which are not counted.
func checkLine(t *testing.T, ctx, line string, w int, plain bool) {
	t.Helper()
	if !plain {
		line = sgr.ReplaceAllString(line, "")
	}
	for _, r := range line {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			t.Fatalf("%s: control character %q in %q", ctx, r, line)
		}
	}
	if cols(line) > w {
		t.Fatalf("%s: %d columns, width %d: %q", ctx, cols(line), w, line)
	}
}

func groupRows(n int) []Row {
	g := Row{Path: "/engine/stopped-containers", Bytes: 50_000_000, Source: SrcOptionalOn, Category: "docker", RuleKey: "docker/stopped@1"}
	for i := 0; i < n; i++ {
		g.Members = append(g.Members, fmt.Sprintf("docker:%03d", i))
	}
	return append(fixtureRows()[:2:2], g)
}

func modelWith(rows []Row) Model {
	m, _ := Update(NewModel(true), ScanDoneMsg{Rows: rows, Summary: fixtureSummary()})
	return flat(m)
}

func TestExclusionWhileConfirmOpenNeverDeletesTheRow(t *testing.T) {
	m, eff := keys(listModel(), " xd")
	target := m.Confirm[0]
	if len(eff) != 1 || m.Screen != ScreenConfirm {
		t.Fatalf("setup: %v %v", eff, m.Screen)
	}
	m, _ = Update(m, ExclusionChangedMsg{Path: target, Excluded: true})
	if m.Screen != ScreenList || m.Confirm != nil || m.Status == "" {
		t.Fatalf("modal must close with a status: screen=%v status=%q", m.Screen, m.Status)
	}
	if _, eff = keys(m, "y"); len(eff) != 0 {
		t.Fatalf("y after the exclusion emitted %v", eff)
	}
}

func TestYRevalidatesAStaleConfirmation(t *testing.T) {
	m, _ := keys(listModel(), " d")
	stale := m
	stale.Rows = append([]Row(nil), m.Rows...)
	for i := range stale.Rows {
		if stale.Rows[i].Path == m.Confirm[0] {
			stale.Rows[i].Source, stale.Rows[i].Base = SrcExcluded, SrcAuto
		}
	}
	got, eff := keys(stale, "y")
	if len(eff) != 0 || got.Screen != ScreenList || got.Status == "" {
		t.Fatalf("eff=%v screen=%v status=%q", eff, got.Screen, got.Status)
	}
}

func TestScanDoneClosesConfirmAndIgnoredWhilePending(t *testing.T) {
	m, _ := keys(listModel(), " d")
	m, _ = Update(m, ScanDoneMsg{Rows: fixtureRows(), Summary: fixtureSummary()})
	if m.Screen != ScreenPlaces || m.Confirm != nil {
		t.Fatal("ScanDoneMsg must close the confirmation")
	}
	m, _ = keys(m, "dy") // the selection survives the rescan
	if !m.Result.Pending {
		t.Fatal("setup: not pending")
	}
	n, _ := Update(m, ScanDoneMsg{Rows: nil})
	if !reflect.DeepEqual(n, m) {
		t.Fatal("ScanDoneMsg during a pending deletion must be ignored")
	}
}

func TestScanDoneDropsStaleSelectionAndDuplicates(t *testing.T) {
	m, _ := keys(listModel(), "a")
	rows := fixtureRows()
	for i := range rows {
		if rows[i].Path == "/home/user/.cache/go-build" {
			rows[i].Source = SrcKept
		}
	}
	rows = append(rows, rows[1], rows[1])
	m, _ = Update(m, ScanDoneMsg{Rows: rows, Summary: fixtureSummary()})
	if m.Selected["/home/user/.cache/go-build"] {
		t.Fatal("a row that became unselectable stays selected")
	}
	if len(m.Rows) != len(fixtureRows()) {
		t.Fatalf("duplicates kept: %d rows", len(m.Rows))
	}
	m, _ = keys(m, "ad")
	seen := map[string]bool{}
	for _, p := range m.Confirm {
		if seen[p] {
			t.Fatalf("path %s twice", p)
		}
		seen[p] = true
	}
}

func TestSmallWindowBlocksSelectingAndDeleting(t *testing.T) {
	m, _ := Update(listModel(), ResizeMsg{Width: 70, Height: 20})
	m, eff := keys(m, "a d y <enter>x")
	if m.Screen != ScreenList || len(eff) != 0 || len(m.Selected) != 0 {
		t.Fatalf("screen=%v eff=%v selected=%v", m.Screen, eff, m.Selected)
	}
	m, _ = Update(m, ResizeMsg{Width: 80, Height: 24})
	m, _ = keys(m, "ad")
	if m.Screen != ScreenConfirm {
		t.Fatal("works again once the window is large enough")
	}
	m, _ = Update(m, ResizeMsg{Width: 70, Height: 20})
	if _, eff = keys(m, "y"); len(eff) != 0 {
		t.Fatalf("y in a small window emitted %v", eff)
	}
}

func TestGroupOf200MembersListsEveryIDOrRefusesY(t *testing.T) {
	m := modelWith(groupRows(200))
	for i, r := range m.visible() {
		if len(r.Members) > 0 {
			m.Cursor = i
		}
	}
	m, _ = keys(m, " d")
	if m.Screen != ScreenConfirm {
		t.Fatal("no confirmation")
	}
	screen := strings.Join(View(m, 80, 24), "\n")
	if !strings.Contains(screen, "and ") || !strings.Contains(screen, " more") || !strings.Contains(screen, "200 containers") {
		t.Fatalf("truncation must be announced:\n%s", screen)
	}
	if got, eff := keys(m, "y"); len(eff) != 0 || got.Screen != ScreenConfirm {
		t.Fatalf("y must be refused while the list is cut: %v", eff)
	}
	m, _ = Update(m, ResizeMsg{Width: 80, Height: 300})
	all := strings.Join(View(m, 80, 300), "\n")
	for _, id := range m.selected()[0].Members {
		if !strings.Contains(all, id) {
			t.Fatalf("id %s not listed", id)
		}
	}
	_, eff := keys(m, "y")
	if len(eff) != 1 {
		t.Fatalf("effects: %v", eff)
	}
	d := eff[0].(Delete)
	if len(d.Paths) != 200 || d.Paths[0] != "docker:000" || d.Paths[199] != "docker:199" {
		t.Fatalf("Delete.Paths = %d paths, first %q", len(d.Paths), d.Paths[0])
	}
	for _, p := range d.Paths {
		if p == "/engine/stopped-containers" {
			t.Fatal("the group path itself is in Delete.Paths")
		}
	}
	if !strings.Contains(strings.Join(View(m, 80, 300), "\n"), "Delete 200 items") {
		t.Fatal("the confirmation counts members, not 1")
	}
}

func TestGroupDeleteDone(t *testing.T) {
	m := modelWith(groupRows(3))
	removed := func(ids ...string) Model {
		n, _ := Update(m, DeleteDoneMsg{Result: Result{Removed: ids}})
		return n
	}
	count := func(n Model) int {
		for _, r := range n.Rows {
			if len(r.Members) > 0 {
				return len(r.Members)
			}
		}
		return -1
	}
	if got := count(removed("docker:000", "docker:001")); got != 1 {
		t.Fatalf("partial removal keeps the remaining member, has %d", got)
	}
	if got := count(removed("docker:000", "docker:001", "docker:002")); got != -1 {
		t.Fatal("group row must go when all members are removed")
	}
}

func TestHostileTextIsSanitisedAndCountedInColumns(t *testing.T) {
	evil := "/日本語/x\t\x1b[2J\n\u0085end"
	rows := append(fixtureRows(), Row{Path: evil, Bytes: 5, Source: SrcAuto, Category: "c\x1b[31m", RuleKey: "r\tk", Why: "why\x1b]0;x\x07",
		Guards: []string{"g\x1b[2J"}, Children: []Child{{"日本語\x1b[2J", 1}}, Members: []string{"docker:\x1b[2Jid\n"}})
	rows[0].Block = "blocked\x1b[2J"
	for _, plain := range []bool{true, false} {
		list := modelWith(rows)
		list.Plain = plain
		list.Status = "status\x1b[2J" + strings.Repeat("日", 100)
		screens := map[string]Model{"list": list}
		for name, script := range map[string]string{"detail": "<enter>", "filter": "/\x1b[2J日本", "confirm": "ad"} {
			screens[name], _ = keys(list, script)
		}
		res, _ := keys(list, "ady")
		res, _ = Update(res, DeleteDoneMsg{Result: Result{Freed: 1, Removed: []string{evil}, Err: "e\x1b[2J日本語",
			Kept: []KeptItem{{evil, "why\t\x1b[2J" + strings.Repeat("日", 80)}}}})
		screens["result"] = res
		for name, m := range screens {
			for _, sz := range [][2]int{{80, 24}, {97, 31}, {200, 60}} {
				for _, l := range View(m, sz[0], sz[1]) {
					checkLine(t, fmt.Sprintf("%s plain=%v %dx%d", name, plain, sz[0], sz[1]), l, sz[0], plain)
				}
			}
		}
	}
}

func TestLongFilterAndWhyFitTheWidth(t *testing.T) {
	m := listModel()
	m.Status = strings.Repeat("s", 100)
	m, _ = keys(m, "/"+strings.Repeat("f", 100))
	for _, l := range View(m, 80, 24) {
		if width(l) > 80 {
			t.Fatalf("line of %d columns: %q", width(l), l)
		}
	}
	d, _ := keys(listModel(), "<enter>")
	d.Rows = append([]Row(nil), d.Rows...)
	d.Rows[0].Why = strings.Repeat("why ", 60)
	for _, l := range View(d, 80, 24) {
		if width(l) > 80 {
			t.Fatalf("detail line of %d columns", width(l))
		}
	}
}

func TestUnknownSizesSayAtLeast(t *testing.T) {
	rows := fixtureRows()
	rows[0].Bytes = -1
	m, _ := keys(modelWith(rows), "ad")
	screen := strings.Join(View(m, 80, 24), "\n")
	if !strings.Contains(screen, "at least") || !strings.Contains(screen, "plus 1 of unknown size") {
		t.Fatalf("confirmation:\n%s", screen)
	}
	m, _ = keys(m, "n")
	if s := strings.Join(View(m, 80, 24), "\n"); !strings.Contains(s, "disk free: 37.3 GB -> at least") {
		t.Fatalf("summary:\n%s", s)
	}
}

func TestFilterMatchesShownSourceText(t *testing.T) {
	m, _ := keys(listModel(), "/optional · on")
	if got := pathsOf(m.visible()); len(got) != 2 {
		t.Fatalf("shown label does not match: %v", got)
	}
	m, _ = keys(listModel(), "/optional-on")
	if got := pathsOf(m.visible()); len(got) != 2 {
		t.Fatalf("plain label does not match: %v", got)
	}
}

func TestPausedDShowsStatus(t *testing.T) {
	m, _ := keys(pausedModel(), "ad")
	if m.Screen != ScreenList || m.Status != "paused: run sapulidi resume" {
		t.Fatalf("screen=%v status=%q", m.Screen, m.Status)
	}
	m, _ = keys(m, "<enter>d")
	if m.Screen != ScreenDetail || !strings.Contains(strings.Join(View(m, 80, 24), "\n"), "paused") {
		t.Fatal("paused status not visible in the detail")
	}
}

// ---- property test with an independent oracle ----

func oracleLess(sortKey SortKey, a, b Row) bool {
	// oldest first, an unknown (zero) time last
	timeLess := func(x, y time.Time) (decided, less bool) {
		switch {
		case x.IsZero() && y.IsZero(), x.Equal(y):
			return false, false
		case x.IsZero():
			return true, false
		case y.IsZero():
			return true, true
		}
		return true, x.Before(y)
	}
	switch sortKey {
	case SortSize:
		if a.Bytes != b.Bytes {
			return a.Bytes > b.Bytes
		}
	case SortAge:
		if d, l := timeLess(a.Created, b.Created); d {
			return l
		}
	case SortUpdated:
		if d, l := timeLess(a.Updated, b.Updated); d {
			return l
		}
	}
	return a.Path < b.Path
}

// oracle is the delete list the user must see: written from the spec, not from the model's own helpers.
func oracle(m Model) []string {
	// the selection is global: no screen, search, `o` or `g` takes part; the order is largest first, then path
	var rows []Row
	for _, r := range m.Rows {
		if (r.Source == "auto" || r.Source == "optional-on" || r.Source == "optional-off") && r.Block == "" && m.Selected[r.Path] {
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return oracleLess(SortSize, rows[i], rows[j]) })
	var out []string
	seen := map[string]bool{}
	for _, r := range rows {
		ps := r.Members
		if len(ps) == 0 {
			ps = []string{r.Path}
		}
		for _, p := range ps {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func randomRows(rng *rand.Rand) []Row {
	rows := fixtureRows()
	rng.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	rows = rows[:3+rng.Intn(len(rows)-2)]
	if rng.Intn(2) == 0 {
		rows = append(rows, rows[rng.Intn(len(rows))]) // duplicate path
	}
	if rng.Intn(2) == 0 {
		g := groupRows(1 + rng.Intn(4))[2]
		if rng.Intn(2) == 0 {
			g.Members = append(g.Members, "docker:abc123") // overlaps the fixture group
		}
		rows = append(rows, g)
	}
	if rng.Intn(3) == 0 {
		i := rng.Intn(len(rows))
		rows[i].Source, rows[i].Base = SrcExcluded, SrcAuto
	}
	if rng.Intn(3) == 0 {
		rows[rng.Intn(len(rows))].Bytes = -1
	}
	return rows
}

func TestPropertyDeleteEqualsIndependentOracle(t *testing.T) {
	alphabet := []string{"j", "k", " ", "a", "x", "s", "o", "/", "d", "y", "n", "q", "e", "g", "<esc>", "<enter>", "<up>", "<down>", "<bs>", "c", "c"}
	sizes := []ResizeMsg{{70, 20}, {80, 24}, {120, 40}, {80, 300}}
	for seq := range 400 {
		rng := rand.New(rand.NewSource(int64(seq)))
		m, _ := Update(NewModel(true), ScanDoneMsg{Rows: fixtureRows(), Summary: fixtureSummary()}) // starts on the places
		for step := range 80 {
			prev := m
			var msg Msg
			switch r := rng.Intn(20); {
			case r == 0:
				msg = ResizeMsg(sizes[rng.Intn(len(sizes))])
			case r == 1:
				msg = ScanDoneMsg{Rows: randomRows(rng), Summary: fixtureSummary()}
			case r == 2 || r == 3:
				all := fixtureRows()
				p := all[rng.Intn(len(all))].Path
				if rng.Intn(4) == 0 {
					p = "docker:abc123"
				}
				msg = ExclusionChangedMsg{Path: p, Excluded: rng.Intn(3) != 0}
			default:
				var eff []Effect
				key := alphabet[rng.Intn(len(alphabet))]
				m, eff = keys(m, key)
				// Switching views, o, g, search, sort and moving never change the selection; only space and a add to
				// it (while a search is being typed they are text), and x and d never touch it.
				if key != " " && key != "a" || prev.Filtering {
					if !reflect.DeepEqual(m.Selected, prev.Selected) {
						t.Fatalf("seq %d step %d: key %q changed the selection on screen %v", seq, step, key, prev.Screen)
					}
				} else if prev.Screen == ScreenPlaces && !prev.small() {
					for p := range m.Selected {
						if prev.Selected[p] {
							continue
						}
						for _, r := range m.Rows {
							if r.Path == p && r.Source != "auto" && r.Source != "optional-on" {
								t.Fatalf("seq %d step %d: places key %q selected a non-ready row %s (%s)", seq, step, key, p, r.Source)
							}
						}
					}
				}
				for _, e := range eff {
					switch e := e.(type) {
					case AddExclusion:
						m, _ = Update(m, ExclusionChangedMsg{Path: e.Path, Excluded: true})
					case RemoveExclusion:
						m, _ = Update(m, ExclusionChangedMsg{Path: e.Path, Excluded: false})
					case Delete:
						want := oracle(prev)
						if prev.Screen != ScreenConfirm || prev.small() || !reflect.DeepEqual(e.Paths, want) || !reflect.DeepEqual(prev.Confirm, want) {
							t.Fatalf("seq %d step %d: Delete %v; oracle %v; confirm %v; screen %v", seq, step, e.Paths, want, prev.Confirm, prev.Screen)
						}
						if rng.Intn(2) == 0 {
							m, _ = Update(m, DeleteDoneMsg{Result: Result{Removed: e.Paths[:len(e.Paths)/2]}})
						} else if n, _ := Update(m, ScanDoneMsg{Rows: randomRows(rng)}); !reflect.DeepEqual(n, m) {
							t.Fatalf("seq %d step %d: ScanDoneMsg changed a model with a pending deletion", seq, step)
						}
					}
				}
			}
			if msg != nil {
				m, _ = Update(m, msg)
			}
			rowSel := map[string]bool{}
			for _, r := range m.Rows {
				if rowSel[r.Path] {
					t.Fatalf("seq %d step %d: duplicate row %s", seq, step, r.Path)
				}
				rowSel[r.Path] = r.Block == "" && (r.Source == "auto" || r.Source == "optional-on" || r.Source == "optional-off")
			}
			for p := range m.Selected {
				if !rowSel[p] {
					t.Fatalf("seq %d step %d: %s is selected but cannot be", seq, step, p)
				}
			}
			if m.Screen == ScreenConfirm {
				if !reflect.DeepEqual(m.Confirm, oracle(m)) {
					t.Fatalf("seq %d step %d: confirmation %v differs from oracle %v", seq, step, m.Confirm, oracle(m))
				}
				text, _ := confirmLines(m, 300, 400)
				all := strings.Join(text, "\n")
				for _, p := range m.Confirm {
					if !strings.Contains(all, p) {
						t.Fatalf("seq %d step %d: the confirmation does not list %s", seq, step, p)
					}
				}
			}
		}
	}
}
