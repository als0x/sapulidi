package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
)

// labelRows has every label, every agent place, a switched-off recommended row, a wholesale row and an excluded row.
func labelRows() []Row {
	agent := func(path, loc string, bytes int64, src string, l core.Label) Row {
		return Row{Path: path, Bytes: bytes, Updated: day(2026, 10, 1), Source: src, Label: l, Location: loc,
			Category: "agent", RuleKey: "agent/" + loc + "@1"}
	}
	return []Row{
		agent("/private/tmp/claude-1000/proj/s1", "claude-scratch", 2_000_000_000, SrcAuto, core.LabelSafe),
		agent("/home/user/.claude/projects/old", "claude-transcripts", 60_000_000, SrcOptionalOn, core.LabelYourCall),
		agent("/home/user/.local/share/claude/versions/2.0.1", "claude-versions", 900_000_000, SrcOptionalOn, core.LabelRecommended),
		agent("/home/user/.claude/plugins/cache/mkt/plug/1.0.0", "claude-plugin-cache", 40_000_000, SrcOptionalOn, core.LabelRecommended),
		agent("/home/user/.codex/.tmp/x", "codex-tmp", 30_000_000, SrcAuto, core.LabelSafe),
		agent("/home/user/.codex/sessions/2025", "codex-sessions", 500_000_000, SrcOptionalOn, core.LabelYourCall),
		{Path: "/home/user/proj/node_modules", Bytes: 1_200_000_000, Updated: day(2026, 9, 1), Source: SrcOptionalOn, Label: core.LabelRecommended, Category: "node", RuleKey: "node/node-modules@2"},
		{Path: "/home/user/proj/dist", Bytes: 70_000_000, Updated: day(2026, 9, 1), Source: SrcOptionalOn, Label: core.LabelYourCall, Category: "node", RuleKey: "node/dist@1"},
		{Path: "/home/user/proj/.worktrees/a1", Bytes: -1, Source: SrcKept, Guard: "dirty", Category: "git", RuleKey: "git/wt@1"},
		{Path: "/home/user/off/node_modules", Bytes: 800_000_000, Source: SrcOptionalOff, Label: core.LabelRecommended, Category: "node", RuleKey: "node/node-modules@2"},
		{Path: "/home/user/ex/target", Bytes: 5_000_000, Source: SrcExcluded, Base: SrcAuto, Label: core.LabelSafe, Category: "rust", RuleKey: "rust/target@1"},
		{Path: "npm cache clean", Bytes: 50_000_000, Source: SrcAuto, Label: core.LabelSafe, Category: "node", RuleKey: "node/npm@1", Block: containersReason},
	}
}

// wantPreselected is the oracle's safe and recommended set of labelRows, written out by hand.
var wantPreselected = []string{
	"/home/user/.claude/plugins/cache/mkt/plug/1.0.0",
	"/home/user/.codex/.tmp/x",
	"/home/user/.local/share/claude/versions/2.0.1",
	"/home/user/proj/node_modules",
	"/private/tmp/claude-1000/proj/s1",
}

// labelModel is the start state: the preselection is kept.
func labelModel() Model {
	m, _ := Update(NewModel(true), ScanDoneMsg{Rows: labelRows(), Summary: placesSummary()})
	m, _ = Update(m, UncoveredMsg{})
	return m
}

func selectedPaths(m Model) []string {
	out := []string{}
	for p, on := range m.Selected {
		if on {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

func TestStartPreselectsSafeAndRecommended(t *testing.T) {
	m := labelModel()
	if got := selectedPaths(m); !slices.Equal(got, wantPreselected) {
		t.Fatalf("selected %v, want %v", got, wantPreselected)
	}
	var oracleSet []string
	for _, r := range fixtureRows() {
		if isReady(r) {
			oracleSet = append(oracleSet, r.Path)
		}
	}
	slices.Sort(oracleSet)
	start, _ := Update(NewModel(true), ScanDoneMsg{Rows: fixtureRows(), Summary: fixtureSummary()})
	if got := selectedPaths(start); !slices.Equal(got, oracleSet) {
		t.Fatalf("selected %v, oracle %v", got, oracleSet)
	}
	if s := screen(m); !strings.Contains(s, "5 of 9 selected") {
		t.Fatalf("summary:\n%s", s)
	}
}

func TestRescanDoesNotSelectAgain(t *testing.T) {
	m, _ := keys(labelModel(), "c")
	m.Selected = map[string]bool{}
	m, _ = Update(m, ScanDoneMsg{Rows: labelRows(), Summary: placesSummary()})
	if got := selectedPaths(m); len(got) != 0 {
		t.Fatalf("a rescan selected %v", got)
	}
}

func TestConfirmationListsEveryPreselectedRow(t *testing.T) {
	m := labelModel()
	for name, script := range map[string]string{"places": "d", "inside a place": "<enter>d", "filtered": "/proj<enter>d"} {
		c, _ := keys(m, script)
		if c.Screen != ScreenConfirm {
			t.Fatalf("%s: screen %v", name, c.Screen)
		}
		got := slices.Clone(c.Confirm)
		slices.Sort(got)
		if !slices.Equal(got, wantPreselected) {
			t.Fatalf("%s: confirm %v", name, got)
		}
		text := strings.Join(View(c, 200, 60), "\n")
		for _, p := range wantPreselected {
			if !strings.Contains(text, p) {
				t.Fatalf("%s: %s is not listed:\n%s", name, p, text)
			}
		}
		if name != "places" && !strings.Contains(text, "are not shown in the list you came from") {
			t.Fatalf("%s:\n%s", name, text)
		}
	}
}

func TestAgentRowsShareAPlacePerAgentAndWhat(t *testing.T) {
	names := placeNames(modelOf(labelRows(), placesSummary()))
	for _, want := range []string{"Claude Code · scratch", "Claude Code · transcripts", "Claude Code · versions",
		"Claude Code · plugin cache", "Codex · tmp", "Codex · sessions"} {
		if !slices.Contains(names, want) {
			t.Errorf("no place %q in %v", want, names)
		}
	}
	// the location decides, not the folder
	r := Row{Path: "/home/user/proj/x", Location: "codex-sessions"}
	if got := placeOfRow(r, []string{"/home/user/proj"}, nil, "/home/user"); got != "Codex · sessions" {
		t.Errorf("place %q", got)
	}
	if got := placeText(NewModel(true), "Codex · sessions"); got != "Codex | sessions" {
		t.Errorf("plain %q", got)
	}
}

func TestAgentPlaceNameFitsAt80(t *testing.T) {
	text := strings.Join(View(labelModel(), 80, 24), "\n")
	if !strings.Contains(text, "Claude Code | plugin cache") {
		t.Fatalf("agent place is cut:\n%s", text)
	}
}

func TestScanSetsLocationFromTheRule(t *testing.T) {
	rows := []Row{{Path: "/a", RuleKey: "x/mine@3"}, {Path: "/b", RuleKey: "x/other@1"}}
	setLocations(rows, []core.Rule{{Pack: "x", ID: "mine", Rev: 3, Location: "codex-tmp"}})
	if rows[0].Location != "codex-tmp" || rows[1].Location != "" {
		t.Fatalf("%+v", rows)
	}
}

func TestRowsOfCarriesTheLabel(t *testing.T) {
	rows := rowsOf(engine.Summary{
		WouldRemove: []core.Candidate{{Path: "/h/a", RuleKey: "node/nm@1", Source: SrcOptionalOn, Label: core.LabelRecommended}},
		Kept:        []core.Kept{{Path: "/h/b", RuleKey: "node/nm@1", Why: "excluded"}},
	})
	if rows[0].Label != core.LabelRecommended || rows[1].Label != core.LabelRecommended {
		t.Fatalf("%+v", rows)
	}
	m := modelOf(rows, placesSummary())
	m, _ = Update(m, ExclusionChangedMsg{Path: "/h/b"})
	if got := m.Rows[1].label(); got != core.LabelRecommended {
		t.Fatalf("un-excluded label %q", got)
	}
	if m.Selected["/h/b"] {
		t.Fatal("an un-excluded row is selected without a key")
	}
}

func TestLabelOfARow(t *testing.T) {
	for _, c := range []struct {
		row  Row
		want core.Label
	}{
		{Row{Source: SrcAuto, Label: core.LabelSafe}, core.LabelSafe},
		{Row{Source: SrcAuto}, core.LabelYourCall},
		{Row{Source: SrcAuto, Label: core.LabelProtected}, core.LabelYourCall},
		{Row{Source: SrcAuto, Label: "bogus"}, core.LabelYourCall},
		{Row{Source: SrcOptionalOn}, core.LabelYourCall},
		{Row{Source: SrcOptionalOn, Label: core.LabelRecommended}, core.LabelRecommended},
		{Row{Source: SrcOptionalOff, Label: core.LabelRecommended}, core.LabelYourCall},
		{Row{Source: SrcKept, Label: core.LabelSafe}, core.LabelProtected},
		{Row{Source: SrcExcluded, Label: core.LabelSafe}, core.LabelProtected},
	} {
		if got := c.row.label(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.row, got, c.want)
		}
	}
}

func TestPlacesColumnsUseLabelWords(t *testing.T) {
	narrow := strings.Join(View(labelModel(), 80, 24), "\n")
	wide := strings.Join(View(labelModel(), 120, 40), "\n")
	head := func(s string) string {
		for _, l := range strings.Split(s, "\n") {
			if strings.Contains(l, "updatedAt") {
				return strings.Join(strings.Fields(l), " ")
			}
		}
		return ""
	}
	if got := head(narrow); got != "safe rec. your call place prot. updatedAt" {
		t.Errorf("80: %q", got)
	}
	if got := head(wide); got != "safe recommended your call place protected updatedAt" {
		t.Errorf("120: %q", got)
	}
}

func TestFlatListShowsLabelWords(t *testing.T) {
	m, _ := keys(labelModel(), "cog")
	text := strings.Join(View(m, 80, 40), "\n")
	for _, want := range []string{"label", " safe", " recommended", " your call", "protected-dirty", "protected-excluded"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q:\n%s", want, text)
		}
	}
	m.Plain = false
	if s := strings.Join(View(m, 80, 40), "\n"); !strings.Contains(s, "protected · dirty") {
		t.Errorf("colour:\n%s", s)
	}
}

func TestHeadlineShortensWhenItDoesNotFit(t *testing.T) {
	m := labelModel()
	full := "Free now: 1.9 GB safe | 2.0 GB recommended | 1.3 GB your call | 1 protected"
	short := "1.9 GB safe | 2.0 GB rec. | 1.3 GB your call | 1 kept"
	if got := headline(m, 116); got != full {
		t.Errorf("wide: %q", got)
	}
	if got := headline(m, cols(full)); got != full {
		t.Errorf("exact fit: %q", got)
	}
	if got := headline(m, cols(full)-1); got != short {
		t.Errorf("one short: %q", got)
	}
	if s := strings.Join(View(m, 120, 40), "\n"); !strings.Contains(s, full) {
		t.Errorf("120:\n%s", s)
	}
	rows := labelRows()
	rows[0].Bytes = -1
	for i := range rows {
		if rows[i].Source == SrcOptionalOn {
			rows[i].Bytes *= 100
		}
	}
	want := "28.6 MB+ safe | 199.3 GB rec. | 59.4 GB your call | 1 kept"
	u := modelOf(rows, placesSummary())
	if s := strings.Join(View(u, 80, 24), "\n"); !strings.Contains(s, want) {
		t.Errorf("80:\n%s", s)
	}
}

func TestNoticeLine(t *testing.T) {
	const n = "Sapu Lidi v0.3.1 is out (you have 0.3.0). Update: sapulidi update"
	s := placesSummary()
	s.Notice = n
	with := strings.Join(View(modelOf(labelRows(), s), 80, 24), "\n")
	if !strings.Contains(with, "| "+n) {
		t.Fatalf("no notice line:\n%s", with)
	}
	if without := strings.Join(View(labelModel(), 80, 24), "\n"); strings.Contains(without, "is out") {
		t.Fatalf("notice without one:\n%s", without)
	}
	s.Notice = n + " or /plugin update sapulidi@x"
	long := strings.Join(View(modelOf(labelRows(), s), 80, 24), "\n")
	if !strings.Contains(long, "a new version is out: run sapulidi version") {
		t.Fatalf("a notice wider than the box is cut:\n%s", long)
	}
	if wide := strings.Join(View(modelOf(labelRows(), s), 120, 40), "\n"); !strings.Contains(wide, s.Notice) {
		t.Fatalf("120:\n%s", wide)
	}
}

func TestSummaryCarriesTheNotice(t *testing.T) {
	e := &EngineDeps{Facts: func() Facts { return Facts{Notice: "n"} }}
	if got := e.Summary().Notice; got != "n" {
		t.Fatalf("notice %q", got)
	}
}

func TestAAndSpaceNeverTakeYourCall(t *testing.T) {
	none := labelModel()
	none.Selected = map[string]bool{}
	for _, script := range []string{"a", "ca", "coa", "<enter>a"} {
		m, _ := keys(none, script)
		for _, p := range selectedPaths(m) {
			if !slices.Contains(wantPreselected, p) {
				t.Errorf("%q selected %s", script, p)
			}
		}
		if len(selectedPaths(m)) == 0 {
			t.Errorf("%q selected nothing", script)
		}
	}
}

func TestSpaceOnAYourCallPlaceAsks(t *testing.T) {
	m := onPlace(t, labelModel(), "Codex · sessions")
	m, _ = keys(m, " ")
	if m.PlacePrompt != "Codex · sessions" || m.Selected["/home/user/.codex/sessions/2025"] {
		t.Fatalf("prompt %q", m.PlacePrompt)
	}
	text := strings.Join(View(m, 80, 24), "\n")
	if !strings.Contains(text, "Select 1 row (your call) in Codex | sessions") {
		t.Fatalf("question:\n%s", text)
	}
	m, _ = keys(m, "y")
	if !m.Selected["/home/user/.codex/sessions/2025"] {
		t.Fatal("y did not select")
	}
}

func TestOnlyAnExactSafeOrRecommendedLabelIsSelected(t *testing.T) {
	want := []string{"/h/optional-safe"}
	start, _ := Update(NewModel(true), ScanDoneMsg{Rows: hostileRows(), Summary: placesSummary()})
	if got := selectedPaths(start); !slices.Equal(got, want) {
		t.Fatalf("preselected %v, want %v", got, want)
	}
	for _, script := range []string{"a", " ", "<enter>a", "ca", "coa"} {
		m, _ := keys(scanned(hostileRows(), placesSummary()), script)
		if got := selectedPaths(m); !slices.Equal(got, want) {
			t.Errorf("%q selected %v, want %v", script, got, want)
		}
	}
}

func TestANeverDeselects(t *testing.T) {
	for _, script := range []string{"a", "aa", "<enter>a", "ca", "caa"} {
		if m, _ := keys(labelModel(), script); !slices.Equal(selectedPaths(m), wantPreselected) {
			t.Errorf("%q after the start: %v", script, selectedPaths(m))
		}
	}
	// a your call row picked by hand stays through `a`
	m, _ := keys(onPlace(t, labelModel(), "Codex · sessions"), " ya")
	if !m.Selected["/home/user/.codex/sessions/2025"] || len(selectedPaths(m)) != len(wantPreselected)+1 {
		t.Errorf("selected %v", selectedPaths(m))
	}
	// space still clears
	cleared, _ := keys(onPlace(t, labelModel(), "Codex · tmp"), " ")
	if cleared.Selected["/home/user/.codex/.tmp/x"] {
		t.Error("space did not deselect")
	}
}

func TestLabelGoldens(t *testing.T) {
	flatAll := func() Model { m, _ := keys(labelModel(), "cog"); return m }
	notice := func() Model {
		m := labelModel()
		m.Summary.Notice = "Sapu Lidi v0.3.1 is out (you have 0.3.0). Update: sapulidi update"
		return m
	}
	inPlace := func() Model {
		m, _ := keys(onPlace(t, labelModel(), pProj), "g<enter>")
		return m
	}
	agentPlace := func() Model {
		m, _ := keys(onPlace(t, labelModel(), "Claude Code · versions"), "<enter>")
		return m
	}
	for name, model := range map[string]func() Model{"labels_places": labelModel, "labels_place": inPlace,
		"labels_agent_place": agentPlace, "labels_flat": flatAll, "labels_notice": notice} {
		for _, sz := range [][2]int{{80, 24}, {120, 40}} {
			file := filepath.Join("testdata", fmt.Sprintf("%s_%dx%d.golden", name, sz[0], sz[1]))
			got := strings.Join(View(model(), sz[0], sz[1]), "\n") + "\n"
			if *update {
				if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/tui -update)", err)
			}
			if got != string(want) {
				t.Errorf("screen differs from %s:\n%s", file, got)
			}
		}
	}
}
