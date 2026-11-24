package tui

import (
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
)

func rowFor(t *testing.T, c core.Candidate) Row {
	t.Helper()
	rows := rowsOf(engine.Summary{WouldRemove: []core.Candidate{c}})
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	return rows[0]
}

func TestWholesaleCommandWithPathIsReportOnly(t *testing.T) {
	for _, cmd := range [][]string{
		{"pnpm", "store", "prune"},
		{"xcrun", "simctl", "delete", "unavailable"},
		{"uv", "cache", "prune"},
		{"npm", "cache", "clean"},
	} {
		r := rowFor(t, core.Candidate{Category: "caches", Path: "/h/cache", Removal: core.ToolCommand, Command: cmd})
		if r.Block == "" || r.selectable() {
			t.Errorf("%v: Block=%q selectable=%v, want report-only", cmd, r.Block, r.selectable())
		}
	}
}

func TestPerItemToolCommandStaysSelectable(t *testing.T) {
	c := core.Candidate{Category: "versions", Path: "/h/.rustup/toolchains/1.60", Removal: core.ToolCommand,
		Command: []string{"rustup", "toolchain", "uninstall", "1.60"}}
	if r := rowFor(t, c); r.Block != "" || !r.selectable() {
		t.Errorf("Block=%q selectable=%v, want selectable", r.Block, r.selectable())
	}
}

func TestWholesaleRowKeepsOptionalOffSource(t *testing.T) {
	c := core.Candidate{Category: "caches", Path: "/h/cache", Removal: core.ToolCommand, Source: SrcOptionalOff,
		Command: []string{"pnpm", "store", "prune"}}
	r := rowFor(t, c)
	if r.Source != SrcOptionalOff || r.Block == "" {
		t.Fatalf("Source=%q Block=%q", r.Source, r.Block)
	}
	m := Model{Rows: []Row{r}}
	if len(m.visible()) != 0 {
		t.Fatal("optional-off wholesale row shows in the default list")
	}
	m.ShowOptional = true
	if len(m.visible()) != 1 || m.visible()[0].selectable() {
		t.Fatal("with the toggle the row must show, dim")
	}
}

func unexcluded(t *testing.T, s engine.Summary) Row {
	t.Helper()
	rows := rowsOf(s)
	m := Model{Rows: rows}
	m, _ = Update(m, ExclusionChangedMsg{Path: rows[0].Path, Excluded: false})
	r := m.Rows[0]
	if r.selectable() || len(deletePaths(selectableOf(m.Rows))) != 0 {
		t.Fatalf("row is selectable after un-exclude: %+v", r)
	}
	return r
}

func selectableOf(rows []Row) []Row {
	var out []Row
	for _, r := range rows {
		if r.selectable() {
			out = append(out, r)
		}
	}
	return out
}

func TestUnexcludedWholesaleRowStaysDim(t *testing.T) {
	unexcluded(t, engine.Summary{Kept: []core.Kept{{Category: "caches", RuleKey: "caches/p@1", Path: "/h/cache", Why: "excluded"}}})
	// a sibling of the same rule that is wholesale gives no origin either
	unexcluded(t, engine.Summary{
		WouldRemove: []core.Candidate{{Category: "caches", RuleKey: "caches/p@1", Path: "/h/other", Removal: core.ToolCommand,
			Command: []string{"npm", "cache", "clean"}}},
		Kept: []core.Kept{{Category: "caches", RuleKey: "caches/p@1", Path: "/h/cache", Why: "excluded"}}})
}

func TestUnexcludedSingleOptionalOffCandidateStaysDim(t *testing.T) {
	unexcluded(t, engine.Summary{Kept: []core.Kept{{Category: "node", RuleKey: "node/nm@1", Path: "/h/p/node_modules", Why: "excluded"}}})
}

func TestIsWholesale(t *testing.T) {
	for cmd, want := range map[string]bool{
		"go clean -cache": true, "brew cleanup": true, "rustup toolchain uninstall 1.60": false,
		"foo uninstall x": true, "rustup show uninstall": true, "weird": true,
	} {
		c := core.Candidate{Removal: core.ToolCommand, Command: strings.Fields(cmd)}
		if got := isWholesale(c); got != want {
			t.Errorf("%q: wholesale=%v, want %v", cmd, got, want)
		}
	}
}

func TestUnexcludeRestoresOptionalOffFromScan(t *testing.T) {
	// the excluded row only exists as a Kept entry; its sibling of the same rule shows the category is off
	s := engine.Summary{
		WouldRemove: []core.Candidate{{Category: "node", RuleKey: "node/nm@1", Path: "/h/q/node_modules", Source: SrcOptionalOff}},
		Kept:        []core.Kept{{Category: "node", RuleKey: "node/nm@1", Path: "/h/p/node_modules", Why: "excluded"}},
	}
	rows := rowsOf(s)
	m := Model{Rows: rows}
	m, _ = Update(m, ExclusionChangedMsg{Path: "/h/p/node_modules", Excluded: true})
	m, _ = Update(m, ExclusionChangedMsg{Path: "/h/p/node_modules", Excluded: false})
	if got := m.Rows[1].Source; got != SrcOptionalOff {
		t.Fatalf("source = %q, want %q", got, SrcOptionalOff)
	}
	if len(m.visible()) != 0 {
		t.Fatalf("optional-off rows show in the default list: %d", len(m.visible()))
	}
}
