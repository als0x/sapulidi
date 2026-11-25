package tui

import (
	"reflect"
	"strings"
	"testing"
)

// mixedModel is the places fixture plus one place with a ready row and an optional row.
func mixedModel() Model {
	rows := append(placesRows(),
		Row{Path: "/home/user/mix/a", Bytes: 100_000_000, Source: SrcAuto, Category: "node", RuleKey: "node/nm@1"},
		Row{Path: "/home/user/mix/b", Bytes: 300_000_000, Source: SrcOptionalOff, Category: "node", RuleKey: "node/nm@1"},
	)
	return scanned(rows, placesSummary())
}

const pMix = "~/mix/"

func onPlace(t *testing.T, m Model, name string) Model {
	t.Helper()
	m.ShowKept = true
	for i, p := range m.places() {
		if p.Name == name {
			m.Cursor = i
			return m
		}
	}
	t.Fatalf("no place %q in %v", name, placeNames(m))
	return m
}

func markOf(t *testing.T, m Model, name string) string {
	t.Helper()
	for _, p := range m.places() {
		if p.Name == name {
			return placeMark(p)
		}
	}
	t.Fatalf("no place %q", name)
	return ""
}

func TestPlaceMarkerStates(t *testing.T) {
	m := mixedModel()
	m.ShowKept = true
	for name, want := range map[string]string{pProj: "[ ]", pOld: "[ ]", pMix: "[ ]", pKept: "[-]", pCmd: "[-]"} {
		if got := markOf(t, m, name); got != want {
			t.Errorf("before: %s is %s, want %s", name, got, want)
		}
	}
	// ready-only
	m = onPlace(t, m, pProj)
	m, _ = keys(m, " ")
	if got := markOf(t, m, pProj); got != "[x]" {
		t.Errorf("ready-only after space: %s", got)
	}
	// optional-only: prompt, then y
	m = onPlace(t, m, pOld)
	m, _ = keys(m, " ")
	if got := markOf(t, m, pOld); got != "[ ]" {
		t.Errorf("optional-only while asking: %s", got)
	}
	m, _ = keys(m, "y")
	if got := markOf(t, m, pOld); got != "[x]" {
		t.Errorf("optional-only after y: %s", got)
	}
	// mixed: space takes the ready row only, so the box is partial; picking the optional one inside fills it
	m = onPlace(t, m, pMix)
	m, _ = keys(m, " ")
	if got := markOf(t, m, pMix); got != "[~]" {
		t.Errorf("mixed after space: %s", got)
	}
	m.Selected["/home/user/mix/b"] = true
	if got := markOf(t, m, pMix); got != "[x]" {
		t.Errorf("mixed with everything: %s", got)
	}
	// kept-only and wholesale-only never change
	for _, name := range []string{pKept, pCmd} {
		m = onPlace(t, m, name)
		before := len(m.Selected)
		m, _ = keys(m, " ")
		if got := markOf(t, m, name); got != "[-]" || len(m.Selected) != before || m.PlacePrompt != "" {
			t.Errorf("%s: mark %s, selected %d->%d, prompt %q", name, got, before, len(m.Selected), m.PlacePrompt)
		}
		if m.Status != "protected: nothing here can be removed from this screen" {
			t.Errorf("%s: status %q", name, m.Status)
		}
	}
}

func TestSpaceOnOptionalOnlyPlaceAsks(t *testing.T) {
	m := onPlace(t, placesModel(), pOld)
	m, _ = keys(m, " ")
	m.Plain = true
	got := placesStatus(m, m.places(), 120)
	want := "Select 1 row (your call) in ~/old/ (762.9 MB)?  y: yes | n / esc: no"
	if got != want {
		t.Fatalf("prompt %q, want %q", got, want)
	}
	// every other key is ignored while the prompt is open
	for _, k := range []string{"j", "k", "a", "d", "x", "g", "c", "s", "o", "/", "?", "u", "q", " ", "<enter>", "<down>"} {
		n, eff := keys(m, k)
		if n.PlacePrompt != pOld || len(n.Selected) != 0 || n.Screen != ScreenPlaces || n.Cursor != m.Cursor || len(eff) != 0 {
			t.Fatalf("key %q acted while asking: prompt %q selected %v screen %v", k, n.PlacePrompt, n.Selected, n.Screen)
		}
	}
	y, _ := keys(m, "y")
	if !reflect.DeepEqual(y.Selected, map[string]bool{"/home/user/old/node_modules": true}) || y.PlacePrompt != "" {
		t.Fatalf("y: %v prompt %q", y.Selected, y.PlacePrompt)
	}
	for _, k := range []string{"n", "<esc>"} {
		n, _ := keys(m, k)
		if len(n.Selected) != 0 || n.PlacePrompt != "" {
			t.Fatalf("%s: %v prompt %q", k, n.Selected, n.PlacePrompt)
		}
	}
	// fully selected: space deselects without asking
	again, _ := keys(y, " ")
	if len(again.Selected) != 0 || again.PlacePrompt != "" {
		t.Fatalf("second space: %v prompt %q", again.Selected, again.PlacePrompt)
	}
}

func TestPromptClosesWhenTheWindowGetsSmall(t *testing.T) {
	m := onPlace(t, placesModel(), pOld)
	m, _ = keys(m, " ")
	if m.PlacePrompt == "" {
		t.Fatal("prompt did not open")
	}
	m, _ = Update(m, ResizeMsg{70, 20})
	if m.PlacePrompt != "" {
		t.Fatalf("prompt still open: %q", m.PlacePrompt)
	}
	m, _ = keys(m, "y")
	if len(m.Selected) != 0 {
		t.Fatalf("y selected unseen: %v", m.Selected)
	}
	// a prompt that is somehow open on a small window never acts either
	m.PlacePrompt = pOld
	m, _ = keys(m, "y")
	if len(m.Selected) != 0 || m.PlacePrompt != "" {
		t.Fatalf("small y: %v %q", m.Selected, m.PlacePrompt)
	}
}

func TestPromptClosesOnRescanAndExclusion(t *testing.T) {
	m := onPlace(t, placesModel(), pOld)
	m, _ = keys(m, " ")
	n, _ := Update(m, ScanDoneMsg{Rows: placesRows()[:3], Summary: placesSummary()})
	if n.PlacePrompt != "" || len(n.Selected) != 0 {
		t.Fatalf("rescan: %q %v", n.PlacePrompt, n.Selected)
	}
	n, _ = keys(n, "y")
	if len(n.Selected) != 0 {
		t.Fatalf("y after rescan: %v", n.Selected)
	}
	e, _ := Update(m, ExclusionChangedMsg{Path: "/home/user/old/node_modules", Excluded: true})
	if e.PlacePrompt != "" {
		t.Fatalf("exclusion: %q", e.PlacePrompt)
	}
}

func TestPromptSizeWithUnknownPartUsesPlus(t *testing.T) {
	rows := []Row{
		{Path: "/home/user/old/a", Bytes: 800_000_000, Source: SrcOptionalOff, Category: "node", RuleKey: "node/nm@1"},
		{Path: "/home/user/old/b", Bytes: -1, Source: SrcOptionalOff, Category: "node", RuleKey: "node/nm@1"},
	}
	m := scanned(rows, placesSummary())
	m, _ = keys(m, " ")
	if got := placesStatus(m, m.places(), 120); !strings.Contains(got, "Select 2 rows (your call) in ~/old/ (762.9 MB+)?") {
		t.Fatalf("prompt %q", got)
	}
}

func TestSpaceOnMixedPlaceSelectsReadyOnlyAndSaysSo(t *testing.T) {
	m := onPlace(t, mixedModel(), pMix)
	m, _ = keys(m, " ")
	if !reflect.DeepEqual(m.Selected, map[string]bool{"/home/user/mix/a": true}) || m.PlacePrompt != "" {
		t.Fatalf("selected %v prompt %q", m.Selected, m.PlacePrompt)
	}
	want := "selected 1 row; your call: 1 row (286.1 MB), enter to pick"
	if m.Status != want {
		t.Fatalf("status %q", m.Status)
	}
	// with the your call row already selected, nothing is said about it
	m2 := onPlace(t, mixedModel(), pMix)
	m2.Selected = map[string]bool{"/home/user/mix/b": true}
	m2, _ = keys(m2, " ")
	if strings.Contains(m2.Status, "your call") {
		t.Fatalf("status %q", m2.Status)
	}
}

func init() {
	goldenCases = append(goldenCases, []struct {
		name  string
		model func() Model
	}{
		{"places_prompt", func() Model { return onPlaceNoT(placesModel(), pOld, " ") }},
		{"places_mixed_selected", func() Model { return onPlaceNoT(mixedModel(), pMix, " ") }},
	}...)
}

func onPlaceNoT(m Model, name, script string) Model {
	for i, p := range m.places() {
		if p.Name == name {
			m.Cursor = i
		}
	}
	m, _ = keys(m, script)
	return m
}
