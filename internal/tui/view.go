package tui

import (
	"fmt"
	"strings"
)

const (
	minWidth  = 80
	minHeight = 24
)

var sortNames = [...]string{"size, largest first", "age, oldest first", "updatedAt, stalest first", "path"}

// View renders the model into at most height lines of at most width terminal columns each (the colour codes of a
// non-plain model are not counted). Every user-supplied text is sanitised: control characters show as '?'.
func View(m Model, width, height int) []string {
	if width < 1 || height < 1 {
		return nil
	}
	var lines []string
	switch {
	case width < minWidth || height < minHeight:
		lines = tooSmall(width, height)
	case m.Screen == ScreenScanning:
		lines = viewScanning(m, width, height)
	case m.Screen == ScreenPlaces:
		lines = viewPlaces(m, width, height)
	case m.Screen == ScreenHelp:
		lines = viewHelp(m, width, height)
	case m.Screen == ScreenUnknown:
		lines = viewUnknown(m, width, height)
	case m.Screen == ScreenDetail:
		lines = viewDetail(m, width, height)
	case m.Screen == ScreenConfirm:
		lines, _ = confirmLines(m, width, height)
	case m.Screen == ScreenResult:
		lines = viewResult(m, width, height)
	default:
		lines = viewList(m, width, height)
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

func tooSmall(w, h int) []string {
	return []string{
		cut("make the window larger", w),
		cut(fmt.Sprintf("(need %dx%d, have %dx%d)", minWidth, minHeight, w, h), w),
	}
}

func arrow(plain bool) string {
	if plain {
		return "->"
	}
	return "→"
}

// panel draws a titled box w columns wide.
func panel(plain bool, title string, body []string, w int) []string {
	tl, tr, bl, br, h, v := "┌", "┐", "└", "┘", "─", "│"
	if plain {
		tl, tr, bl, br, h, v = "+", "+", "+", "+", "-", "|"
	}
	head := tl + h + " " + title + " "
	out := []string{head + repeat(h, w-1-cols(head)) + tr}
	for _, l := range body {
		out = append(out, v+" "+fit(l, w-4)+" "+v)
	}
	return append(out, bl+repeat(h, w-2)+br)
}

func center(block []string, w, h int) []string {
	bw := 0
	for _, l := range block {
		bw = max(bw, cols(l))
	}
	left := repeat(" ", max((w-bw)/2, 0))
	out := make([]string, 0, h)
	for i := 0; i < (h-len(block))/2; i++ {
		out = append(out, "")
	}
	for _, l := range block {
		out = append(out, cut(left+l, w))
	}
	return out
}

func viewScanning(m Model, w, h int) []string {
	block := []string{"sapulidi: scanning", ""}
	if len(m.Scan) == 0 {
		block = append(block, "starting...")
	}
	for _, s := range m.Scan {
		const barW = 20
		done := 0
		if s.Total > 0 {
			done = min(s.Done*barW/s.Total, barW)
		}
		block = append(block, fit(s.Name, 16)+" ["+repeat("#", done)+repeat("-", barW-done)+"] "+fmt.Sprintf("%d/%d", s.Done, s.Total))
	}
	block = append(block, "", "esc: cancel")
	return center(block, w, h)
}

// hiddenLines are the summary lines that say what the default list leaves out; none when nothing is hidden.
func hiddenLines(m Model) []string {
	var optN, keptN, optUnknown int
	var optBytes int64
	for _, r := range m.Rows {
		if !m.hidden(r) {
			continue
		}
		if r.Source == SrcKept {
			keptN++
			continue
		}
		if r.Block != "" {
			continue // a wholesale row never becomes selectable, `o` does not help
		}
		optN++
		if r.Bytes < 0 {
			optUnknown++
		} else {
			optBytes += r.Bytes
		}
	}
	var out []string
	if optN > 0 {
		size := fmtBytes(optBytes)
		if optUnknown > 0 {
			size += "+"
		}
		d := dot(m.Plain)
		out = append(out, fmt.Sprintf("optional (off): %d %s %s %s o", optN, d, size, d))
	}
	if keptN > 0 {
		out = append(out, fmt.Sprintf("kept: %d %s g", keptN, dot(m.Plain)))
	}
	return out
}

func summaryLines(m Model) []string {
	var excluded, selectable int
	for _, r := range m.Rows {
		if r.Source == SrcExcluded {
			excluded++
		}
	}
	for _, r := range m.Rows { // the selection is global: so is the count it is shown against
		if r.selectable() {
			selectable++
		}
	}
	sel := m.selected()
	known, unknown := sumBytes(sel)
	total := fmtBytes(known)
	if unknown > 0 {
		total = "at least " + total
	}
	sched := "schedule: off"
	if m.Summary.Interval > 0 {
		sched = "schedule: every " + fmtInterval(m.Summary.Interval)
		if !m.Summary.Next.IsZero() {
			sched += ", next " + m.Summary.Next.Format("15:04")
		}
	}
	disk := "disk free: -"
	if m.Summary.FreeNow > 0 {
		after := fmtBytes(int64(m.Summary.FreeNow) + known)
		if unknown > 0 {
			after = "at least " + after
		}
		disk = "disk free: " + fmtBytes(int64(m.Summary.FreeNow)) + " " + arrow(m.Plain) + " " + after
	}
	d := dot(m.Plain)
	lines := []string{
		headline(m),
		fmt.Sprintf("to remove: %s, %d of %d selected %s excluded: %d", total, len(sel), selectable, d, excluded),
		fmt.Sprintf("agents installed: %d %s %s", m.Summary.Agents, d, sched),
		disk,
	}
	lines = append(lines, hiddenLines(m)...)
	if u := unknownLine(m); u != "" {
		lines = append(lines, u)
	}
	return lines
}

// headline answers the first question: how much can go now, how much if the user chooses, how much is protected.
// The three numbers come from m.Rows. A wholesale command row and an excluded row are in none of them: neither can be
// deleted from here. A separate excluded count would not fit the 76 columns of the box at 80x24 next to them.
func headline(m Model) string {
	var autoB, optB int64
	var autoU, optU bool
	var kept int
	for _, r := range m.Rows {
		switch {
		case r.Source == SrcKept:
			kept++
		case readyRow(r):
			addBytes(&autoB, &autoU, r.Bytes)
		case r.selectable():
			addBytes(&optB, &optU, r.Bytes)
		}
	}
	d := dot(m.Plain)
	return fmt.Sprintf("Free now: %s automatic %s %s if you choose %s %d protected",
		sizeCell(1, autoB, autoU), d, sizeCell(1, optB, optU), d, kept)
}

// unknownLine is the summary line of the folders no rule covers; "" when there are none and the search finished.
func unknownLine(m Model) string {
	switch m.UnkState {
	case UnkSearching:
		return "not covered by any rule: searching" + ellipsis(m.Plain)
	case UnkCancelled:
		return "not covered by any rule: search cancelled"
	}
	if len(m.Summary.Unknown) == 0 && !m.Summary.UnknownPartial {
		return ""
	}
	s := fmt.Sprintf("not covered by any rule: %s %s %s", plural(len(m.Summary.Unknown), "folder"), dot(m.Plain), unknownTotal(m))
	if m.Summary.UnknownPartial {
		s += " partial"
	}
	return s + "  (u)"
}

func ellipsis(plain bool) string {
	if plain {
		return "..."
	}
	return "…"
}

// unknownTotal is the total size of the uncovered folders; a partial search only gives lower bounds, so it ends in
// "+" like every other size that is not final.
func unknownTotal(m Model) string {
	var total int64
	for _, u := range m.Summary.Unknown {
		total += u.Bytes
	}
	return sizeCell(1, total, m.Summary.UnknownPartial)
}

// legend explains the words of the list; first is the word of the removable-now column ("ready" or "auto").
func legend(m Model, first string) string {
	d := dot(m.Plain)
	return first + " = removable now " + d + " optional = you pick it " + d + " kept = protected"
}

// footerOf is the one-line keys footer of the current screen.
func footerOf(m Model) string {
	d := " " + dot(m.Plain) + " "
	join := func(k ...string) string { return strings.Join(k, d) }
	if m.Filtering {
		return join("type to search", "enter keep", "esc clear")
	}
	if m.PlacePrompt != "" {
		return join("y yes", "n or esc no")
	}
	switch m.Screen {
	case ScreenPlaces:
		return join("enter open", "space select", "d delete", "g protected", "c view", "? help", "q quit")
	case ScreenPlace:
		return join("esc back", "enter detail", "space select", "x exclude", "d delete", "? help", "q quit")
	case ScreenHelp:
		return join("up/down scroll", "esc or ? close", "q quit")
	case ScreenUnknown:
		return join("up/down scroll", "esc back", "? help", "q quit")
	}
	return join("enter detail", "space select", "d delete", "o optional", "c view", "? help", "q quit")
}

// topPanels draws the summary at the full width of the window.
func topPanels(m Model, w int) []string {
	return panel(m.Plain, "summary", summaryLines(m), w)
}

func viewList(m Model, w, h int) []string {
	lines := topPanels(m, w)

	pw := w - 49
	head := " " + "    " + fitRight("size", 9) + " " + fit("path", pw) + " " + fit("age", 5) + " " + fit("updatedAt", 10) + " " + fit("source", 16)
	lines = append(lines, style(m.Plain, "1", head), cut("  "+legend(m, "auto"), w))

	vis := m.visible()
	rowsH := h - len(lines) - 2
	top := min(max(m.Cursor-rowsH/2, 0), max(len(vis)-rowsH, 0))
	if len(vis) == 0 {
		if m.Filter != "" {
			lines = append(lines, "  nothing matches the search (esc clears it)")
		} else {
			lines = append(lines, "  nothing is ready to delete")
			for _, l := range hiddenLines(m) {
				lines = append(lines, "  "+l)
			}
		}
	}
	for i := top; i < len(vis) && i < top+rowsH; i++ {
		lines = append(lines, listRow(m, vis[i], i == m.Cursor, pw))
	}
	for len(lines) < h-2 {
		lines = append(lines, "")
	}
	return append(lines, statusLine(m, vis, w), cut(footerOf(m), w))
}

func listRow(m Model, r Row, cur bool, pw int) string {
	mark, box := " ", "[ ]"
	switch {
	case !r.selectable():
		box = "[-]"
	case m.Selected[r.Path]:
		box = "[x]"
	}
	if cur {
		mark = ">"
	}
	line := mark + box + " " + fitRight(fmtBytes(r.Bytes), 9) + " " + fit(shorten(r.Path, pw), pw) + " " +
		fit(fmtAge(r.Created, m.Summary.Now), 5) + " " + fit(fmtDate(r.Updated), 10) + " " +
		fit(sourceText(r, m.Plain), 16)
	switch {
	case cur:
		return style(m.Plain, "7", line)
	case !r.selectable():
		return style(m.Plain, "2", line)
	}
	return line
}

func statusLine(m Model, vis []Row, w int) string {
	switch {
	case m.Filtering:
		return cut("/"+m.Filter+"_", w)
	case m.Summary.Paused:
		return style(m.Plain, "33", cut("paused: run sapulidi resume", w))
	case m.Status != "":
		return cut(m.Status, w)
	}
	if r, ok := m.cursorRow(); ok && !r.selectable() {
		return cut(r.reason(), w)
	}
	s := fmt.Sprintf("%s, sorted by %s", plural(len(vis), "row"), sortNames[m.Sort])
	if m.Screen == ScreenPlace {
		s = placeText(m, m.Place) + " " + s
	}
	if m.Filter != "" {
		s = fmt.Sprintf("filter: %s (esc clears), %s", m.Filter, s)
	}
	return cut(s, w)
}

func kv(label, value string, w int) []string {
	parts := wrap(value, w-11)
	out := make([]string, len(parts))
	for i, p := range parts {
		l := ""
		if i == 0 {
			l = label
		}
		out[i] = fit(l, 10) + " " + p
	}
	return out
}

func viewDetail(m Model, w, h int) []string {
	r, ok := m.cursorRow()
	if !ok {
		return []string{"nothing selected", "esc: back"}
	}
	why := r.Why
	if why == "" {
		why = r.reason()
	}
	var lines []string
	add := func(label, value string) { lines = append(lines, kv(label, value, w)...) }
	add("path", r.Path)
	add("size", fmtBytes(r.Bytes))
	add("created", fmtDate(r.Created)+" ("+fmtAge(r.Created, m.Summary.Now)+")")
	add("updatedAt", fmtDate(r.Updated))
	add("category", r.Category+" "+dot(m.Plain)+" "+r.RuleKey)
	add("source", sourceText(r, m.Plain))
	if r.Source == SrcKept && r.Guard != "" {
		add("kept", guardMeaning(r.Guard))
	}
	if len(r.Members) > 0 {
		add("members", fmt.Sprintf("%d", len(r.Members)))
	}
	add("why", why)
	if r.Block != "" || r.Source == SrcExcluded {
		add("blocked", r.reason())
	}
	lines = append(lines, "", "guards that passed")
	if len(r.Guards) == 0 {
		lines = append(lines, "  none recorded")
	}
	for _, g := range r.Guards {
		lines = append(lines, "  "+shorten(g, w-2))
	}
	lines = append(lines, "", "largest inside")
	if len(r.Children) == 0 {
		lines = append(lines, "  nothing listed")
	}
	for i, c := range r.Children {
		if i == 5 {
			break
		}
		lines = append(lines, "  "+fitRight(fmtBytes(c.Bytes), 9)+"  "+shorten(c.Name, w-13))
	}
	footer := "esc: back  space: select  x: exclude  d: delete"
	if m.Status != "" {
		footer = m.Status
	}
	return withFooter(lines, footer, w, h)
}

func dot(plain bool) string {
	if plain {
		return "|"
	}
	return "·"
}

// withFooter fits every line to w and cuts lines so that the footer stays on the last row.
func withFooter(lines []string, footer string, w, h int) []string {
	if len(lines) > h-1 {
		lines = lines[:h-1]
	}
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	out := make([]string, 0, h)
	for _, l := range lines {
		out = append(out, cut(l, w))
	}
	return append(out, cut(footer, w))
}

// notInView returns the rows that the list the confirmation came from does not show: not among the visible rows (the
// flat list, an open place) or not in a listed place (the places).
func (m Model) notInView(rows []Row) []Row {
	var out []Row
	if m.listScreen() == ScreenPlaces {
		shown := map[string]bool{}
		for _, p := range m.places() {
			shown[p.Name] = true
		}
		for _, r := range rows {
			if !shown[r.Place] {
				out = append(out, r)
			}
		}
		return out
	}
	shown := map[string]bool{}
	for _, r := range m.visible() {
		shown[r.Path] = true
	}
	for _, r := range rows {
		if !shown[r.Path] {
			out = append(out, r)
		}
	}
	return out
}

// confirmParts is the confirmation's always-visible header and its scrolling entries, for the rows selected now.
func confirmParts(m Model, w int) (head, entries []string) {
	rows := m.selected()
	paths := deletePaths(rows)
	var optOff int
	for _, r := range rows {
		if r.origin() == SrcOptionalOff {
			optOff++
		}
	}
	known, unknown := sumBytes(rows)
	noun := "items"
	if len(paths) == 1 {
		noun = "item"
	}
	head = []string{fmt.Sprintf("Delete %d %s, %s in total?", len(paths), noun, sizeText(known, unknown))}
	if optOff > 0 {
		head = append(head, fmt.Sprintf("%d of them are optional and currently switched off.", optOff))
	}
	if n := len(deletePaths(m.notInView(rows))); n > 0 {
		head = append(head, fmt.Sprintf("%d of them are not shown in the list you came from.", n))
	}
	head = append(head, "")
	inPlace := m.listScreen() == ScreenPlace
	for _, r := range rows {
		where := ""
		if inPlace && r.Place != m.Place {
			where = " " + shorten(placeText(m, r.Place), 24)
		}
		if len(r.Members) == 0 {
			entries = append(entries, "  "+fitRight(fmtBytes(r.Bytes), 9)+"  "+shorten(r.Path, w-13-cols(where))+where)
			continue
		}
		entries = append(entries, "  "+fitRight(fmtBytes(r.Bytes), 9)+"  "+shorten(r.Path, w-25-cols(where))+fmt.Sprintf(" (%d containers)", len(r.Members))+where)
		for _, id := range r.Members {
			entries = append(entries, "      "+shorten(id, w-6))
		}
	}
	return head, entries
}

// confirmLines draws the confirmation for the rows selected now. The header stays; the entries scroll from
// m.ConfirmTop, and more is true when they do not all fit.
func confirmLines(m Model, w, h int) (lines []string, more bool) {
	head, entries := confirmParts(m, w)
	room := max(h-len(head)-1, 1)
	top := min(max(m.ConfirmTop, 0), max(len(entries)-room, 0))
	end := min(top+room, len(entries))
	lines = append(head, entries[top:end]...)
	footer := "y/enter: delete  n/esc: cancel"
	if more = len(entries) > room; more {
		footer = fmt.Sprintf("rows %d-%d of %d  up/down: scroll  y: delete  n/esc: cancel", top+1, end, len(entries))
	}
	return withFooter(lines, footer, w, h), more
}

func viewResult(m Model, w, h int) []string {
	if m.Result.Pending {
		return withFooter([]string{"deleting..."}, "", w, h)
	}
	r := m.Result
	lines := []string{fmt.Sprintf("Freed %s, removed %d items.", fmtBytes(r.Freed), len(r.Removed))}
	if r.Err != "" {
		lines = append(lines, "error: "+shorten(r.Err, w-7))
	}
	if len(r.Kept) > 0 {
		lines = append(lines, "", fmt.Sprintf("Kept by a guard (%d):", len(r.Kept)))
		for _, k := range r.Kept {
			why := cut(k.Why, w/2)
			lines = append(lines, "  "+shorten(k.Path, w-6-cols(why))+"  "+why)
		}
	}
	return withFooter(lines, "esc: back", w, h)
}
