package tui

import (
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
