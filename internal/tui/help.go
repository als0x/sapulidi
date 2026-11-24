package tui

import (
	"fmt"
	"strings"
)

// helpText is the `?` screen. Plain strings, each under 76 columns; the screen scrolls when the window is short.
var helpText = []string{
	"What you are looking at",
	"",
	"Columns",
	"  ready     removable now: a rule is on and every guard passed",
	"  optional  removable only if you pick it (a rule that is switched off)",
	"  kept      protected: a guard keeps it (open files, unsaved work, ...)",
	"  place     the folder or tool cache the rows belong to",
	"  In the flat list the source column says the same: auto = ready.",
	"",
	"Markers",
	"  [ ] not selected   [x] selected   [~] some rows of a place selected",
	"  [-] nothing here can be selected",
	"",
	"Keys",
	"  enter  open a place, or show the details of a row",
	"  space  select a row; on a place: select its ready rows",
	"  a      select every ready row in view",
	"  g      show or hide protected (kept) rows",
	"  c      switch between the places and the flat list",
	"  x      exclude a row from every future run (open a place first)",
	"  d      delete what is selected, after a confirmation",
	"  u      folders that no rule covers (read-only)",
	"  / search   s sort   esc back   q quit",
	"",
	"A selection counts for the whole scan, whatever the list shows: the",
	"confirmation lists everything that will be deleted.",
	"Kept rows are never removable from here.",
}

// helpRoom is how many help lines fit above the footer.
func helpRoom(h int) int { return max(h-1, 1) }

func viewHelp(m Model, w, h int) []string {
	room := helpRoom(h)
	top := min(max(m.HelpTop, 0), max(len(helpText)-room, 0))
	end := min(top+room, len(helpText))
	footer := "esc or ? closes"
	if len(helpText) > room {
		footer = fmt.Sprintf("lines %d-%d of %d  up/down: scroll  esc or ? closes", top+1, end, len(helpText))
	}
	return withFooter(helpText[top:end], footer, w, h)
}

func helpKey(m Model, k KeyMsg) (Model, []Effect) {
	_, h := m.size()
	last := max(len(helpText)-helpRoom(h), 0)
	switch {
	case k.Special == KeyEsc || k.Rune == '?':
		m.Screen, m.HelpTop = m.HelpFrom, 0
	case k.Special == KeyUp || k.Rune == 'k':
		m.HelpTop = max(m.HelpTop-1, 0)
	case k.Special == KeyDown || k.Rune == 'j':
		m.HelpTop = min(m.HelpTop+1, last)
	case k.Special == KeyPgUp:
		m.HelpTop = max(m.HelpTop-helpRoom(h), 0)
	case k.Special == KeyPgDn:
		m.HelpTop = min(m.HelpTop+helpRoom(h), last)
	}
	return m, nil
}

// openHelp shows the help; the cursor of the screen below is not touched, so closing returns to the same row.
func openHelp(m Model) Model {
	m.HelpFrom, m.Screen, m.HelpTop = m.Screen, ScreenHelp, 0
	return m
}

// uncoveredNote says why the uncovered list cannot be acted on.
const uncoveredNote = "no rule covers these; sapulidi never removes what it does not recognise"

// openUnknown shows the uncovered folders, or says there are none.
func openUnknown(m Model) Model {
	if len(m.Summary.Unknown) == 0 {
		m.Status = "no big folder is left uncovered"
		if m.Summary.UnknownPartial {
			m.Status = "no uncovered folder found yet (the search was partial)"
		}
		return m
	}
	m.UnkFrom, m.Screen, m.UnkTop = m.Screen, ScreenUnknown, 0
	return m
}

func unknownKey(m Model, k KeyMsg) (Model, []Effect) {
	_, h := m.size()
	last := max(len(m.Summary.Unknown)-unknownRoom(h), 0)
	switch {
	case k.Special == KeyEsc:
		m.Screen, m.UnkTop = m.UnkFrom, 0
	case k.Rune == '?':
		m = openHelp(m)
	case k.Rune == 'q':
		return m, []Effect{Quit{}}
	case k.Special == KeyUp || k.Rune == 'k':
		m.UnkTop = max(m.UnkTop-1, 0)
	case k.Special == KeyDown || k.Rune == 'j':
		m.UnkTop = min(m.UnkTop+1, last)
	case k.Rune == ' ' || k.Rune == 'x' || k.Rune == 'd' || k.Rune == 'a':
		m.Status = "read-only: " + uncoveredNote
	}
	return m, nil
}

// unknownRoom is how many folders fit: the screen has a title, the note, a blank line, a status and a footer.
func unknownRoom(h int) int { return max(h-5, 1) }

func viewUnknown(m Model, w, h int) []string {
	var total int64
	for _, u := range m.Summary.Unknown {
		total += u.Bytes
	}
	title := fmt.Sprintf("Not covered by any rule: %s, %s", plural(len(m.Summary.Unknown), "folder"), fmtBytes(total))
	if m.Summary.UnknownPartial {
		title += " (partial: the search did not finish)"
	}
	lines := []string{title, uncoveredNote, ""}
	room := unknownRoom(h)
	top := min(max(m.UnkTop, 0), max(len(m.Summary.Unknown)-room, 0))
	for i := top; i < len(m.Summary.Unknown) && i < top+room; i++ {
		u := m.Summary.Unknown[i]
		lines = append(lines, "  "+fitRight(fmtBytes(u.Bytes), 9)+"  "+shorten(clean(strings.TrimRight(showHome(u.Path, m.Summary.Home), "/")), w-13))
	}
	for len(lines) < h-2 {
		lines = append(lines, "")
	}
	status := m.Status
	return append(withFooter(lines, status, w, h-1), cut(footerOf(m), w))
}
