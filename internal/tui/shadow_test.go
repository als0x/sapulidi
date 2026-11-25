package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The helpers below track the selection with rules written from the spec, never from Model.selected(): the property
// test keeps a parallel map and compares it with Model.Selected after every step, on every screen.

func isSel(r Row) bool {
	return r.Block == "" && (r.Source == "auto" || r.Source == "optional-on" || r.Source == "optional-off")
}

// isReady is a safe or recommended row: an auto row, or a recommended one whose category is not switched off.
func isReady(r Row) bool {
	return r.Block == "" && (r.Source == "auto" || r.Source == "optional-on" && r.Label == "recommended")
}

// shadowKey applies a key press to want. prev is the model before the key; it only says what the key targets
// (the row or place under the cursor, the rows or places in view).
//
// sp is the place the optional-rows question is open for, tracked here by rules of its own (opened by space on an
// optional-only place, closed by y, n, esc, a small window, a rescan, an exclusion or a deletion); it is never read
// from the model.
func shadowKey(want map[string]bool, sp *string, prev Model, key string) {
	if *sp != "" { // the optional-rows question: only y selects, and only a place with nothing ready
		place := *sp
		if prev.small() || key == "y" || key == "n" || key == "<esc>" {
			*sp = ""
		}
		if key != "y" || prev.small() {
			return
		}
		var opt []string
		for _, r := range prev.Rows {
			if r.Place != place {
				continue
			}
			if isReady(r) {
				return
			}
			if isSel(r) {
				opt = append(opt, r.Path)
			}
		}
		for _, p := range opt {
			want[p] = true
		}
		return
	}
	if prev.Screen == ScreenPlaces && !prev.Filtering && !prev.small() && key == " " {
		// an optional-only place asks first; when all its optional rows are selected, space clears them
		if cp, ok := prev.cursorPlace(); ok {
			var opt []string
			ready := false
			for _, r := range prev.Rows {
				if r.Place == cp.Name {
					ready = ready || isReady(r)
					if isSel(r) && !isReady(r) {
						opt = append(opt, r.Path)
					}
				}
			}
			if !ready && len(opt) > 0 {
				all := true
				for _, p := range opt {
					all = all && want[p]
				}
				if all {
					for _, p := range opt {
						delete(want, p)
					}
				} else {
					*sp = cp.Name
				}
				return
			}
		}
	}
	if prev.Filtering || prev.small() || key != " " && key != "a" {
		return // typed text, a blocked key, or a key that never touches the selection
	}
	toggle := func(paths []string) {
		all := len(paths) > 0
		for _, p := range paths {
			all = all && want[p]
		}
		for _, p := range paths {
			if all {
				delete(want, p)
			} else {
				want[p] = true
			}
		}
	}
	cursorRow := func() {
		if r, ok := prev.cursorRow(); ok && isSel(r) {
			toggle([]string{r.Path})
		}
	}
	switch prev.Screen {
	case ScreenPlaces:
		names := map[string]bool{}
		if key == " " {
			cp, ok := prev.cursorPlace()
			if !ok {
				return
			}
			names[cp.Name] = true
		} else {
			for _, p := range prev.places() {
				names[p.Name] = true
			}
		}
		var ready []string
		for _, r := range prev.Rows {
			if names[r.Place] && isReady(r) {
				ready = append(ready, r.Path)
			}
		}
		toggle(ready)
	case ScreenList:
		if key == " " {
			cursorRow()
			return
		}
		for _, r := range prev.visible() {
			if isReady(r) {
				want[r.Path] = true
			}
		}
	case ScreenPlace:
		if key == " " {
			cursorRow()
			return
		}
		for _, r := range prev.visible() {
			if isReady(r) {
				want[r.Path] = true
			}
		}
	case ScreenDetail:
		if key == " " {
			cursorRow()
		}
	}
}

// shadowMsg applies a message to want; m is the model before it.
func shadowMsg(want map[string]bool, sp *string, m Model, msg Msg) {
	switch msg := msg.(type) {
	case ResizeMsg:
		if msg.Width != 0 && (msg.Width < minWidth || msg.Height < minHeight) {
			*sp = ""
		}
	case ExclusionChangedMsg:
		*sp = ""
		if !msg.Excluded {
			return
		}
		for _, r := range m.Rows {
			if r.Path == msg.Path && r.Source != "excluded" || r.Path != msg.Path && slices.Contains(r.Members, msg.Path) {
				delete(want, r.Path)
			}
		}
	case DeleteDoneMsg:
		*sp = ""
		gone := map[string]bool{}
		for _, p := range msg.Result.Removed {
			gone[p] = true
		}
		for _, r := range m.Rows {
			if len(r.Members) == 0 {
				if gone[r.Path] {
					delete(want, r.Path)
				}
				continue
			}
			for _, id := range r.Members {
				if gone[id] {
					delete(want, r.Path)
				}
			}
		}
	case ScanDoneMsg:
		if m.Screen == ScreenResult && m.Result.Pending {
			return // a running deletion comes first
		}
		*sp = ""
		first := map[string]Row{}
		for _, r := range msg.Rows {
			if _, ok := first[r.Path]; !ok {
				first[r.Path] = r
			}
		}
		for p := range want {
			if r, ok := first[p]; !ok || !isSel(r) {
				delete(want, p)
			}
		}
	}
}

// checkConfirmText checks the header of the confirmation (count, total, optional-off line, not-shown line) against
// want, at a window large enough for everything and at 80x24 where the entries scroll.
func checkConfirmText(t *testing.T, ctx string, m Model, want map[string]bool) {
	t.Helper()
	var rows []Row
	for _, r := range m.Rows {
		if want[r.Path] && isSel(r) {
			rows = append(rows, r)
		}
	}
	distinct := func(rs []Row) int {
		seen := map[string]bool{}
		for _, r := range rs {
			if len(r.Members) == 0 {
				seen[r.Path] = true
			}
			for _, id := range r.Members {
				seen[id] = true
			}
		}
		return len(seen)
	}
	var known int64
	var unknown, optOff int
	for _, r := range rows {
		if r.Bytes < 0 {
			unknown++
		} else {
			known += r.Bytes
		}
		if r.Source == "optional-off" {
			optOff++
		}
	}
	shown := map[string]bool{}
	var hidden []Row
	if m.listScreen() == ScreenPlaces {
		for _, p := range m.places() {
			shown[p.Name] = true
		}
		for _, r := range rows {
			if !shown[r.Place] {
				hidden = append(hidden, r)
			}
		}
	} else {
		for _, r := range m.visible() {
			shown[r.Path] = true
		}
		for _, r := range rows {
			if !shown[r.Path] {
				hidden = append(hidden, r)
			}
		}
	}
	n := distinct(rows)
	noun := "items"
	if n == 1 {
		noun = "item"
	}
	head := fmt.Sprintf("Delete %d %s, %s in total?", n, noun, sizeText(known, unknown))
	optLine := fmt.Sprintf("%d of them are in categories you switched off.", optOff)
	hideLine := fmt.Sprintf("%d of them are not shown in the list you came from.", distinct(hidden))

	for _, sz := range [][2]int{{300, 400}, {80, 24}} {
		lines, _ := confirmLines(m, sz[0], sz[1])
		text := strings.Join(lines, "\n")
		if lines[0] != head {
			t.Fatalf("%s (%dx%d): first line %q, want %q", ctx, sz[0], sz[1], lines[0], head)
		}
		if has := strings.Contains(text, optLine); has != (optOff > 0) {
			t.Fatalf("%s (%dx%d): optional-off line present=%v, optional-off rows=%d", ctx, sz[0], sz[1], has, optOff)
		}
		if has := strings.Contains(text, hideLine); has != (len(hidden) > 0) {
			t.Fatalf("%s (%dx%d): not-shown line present=%v, hidden rows=%d\n%s", ctx, sz[0], sz[1], has, len(hidden), text)
		}
	}
	// every path is reachable by scrolling at 80x24
	hd, entries := confirmParts(m, 80)
	room := 24 - len(hd) - 1
	var pages []string
	mm := m
	for top := 0; ; top += room {
		mm.ConfirmTop = top
		lines, _ := confirmLines(mm, 80, 24)
		if !slices.Equal(lines[:len(hd)], hd) {
			t.Fatalf("%s: the header changed while scrolling", ctx)
		}
		pages = append(pages, lines[len(hd):len(lines)-1]...)
		if top+room >= len(entries) {
			break
		}
	}
	all := strings.Join(pages, "\n")
	for _, p := range m.Confirm {
		if len(p) <= 40 && !strings.Contains(all, p) {
			t.Fatalf("%s: %s is not reachable by scrolling at 80x24", ctx, p)
		}
	}
}
