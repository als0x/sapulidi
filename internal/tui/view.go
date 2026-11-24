package tui

import "fmt"

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

func sideBySide(a, b []string, aw, bw int) []string {
	n := max(len(a), len(b))
	out := make([]string, n)
	for i := range out {
		l, r := repeat(" ", aw), repeat(" ", bw)
		if i < len(a) {
			l = a[i]
		}
		if i < len(b) {
			r = b[i]
		}
		out[i] = l + " " + r
	}
	return out
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

var keyHelp = [][2]string{
	{"esc: back", "d: delete"},
	{"/ : search", "s: sort"},
	{"enter: detail", "space: select"},
	{"x: exclude", "a: select all"},
	{"o: optional", "g: guarded"},
	{"q: quit", ""},
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
	for _, r := range m.visible() {
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
	lines := []string{
		fmt.Sprintf("agents installed: %d", m.Summary.Agents),
		fmt.Sprintf("to remove: %s, %d of %d selected", total, len(sel), selectable),
		fmt.Sprintf("excluded: %d", excluded),
		sched,
		disk,
	}
	return append(lines, hiddenLines(m)...)
}

func viewList(m Model, w, h int) []string {
	rw := max(32, w/3)
	lw := w - rw - 1
	left := panel(m.Plain, "summary", summaryLines(m), lw)
	keys := make([]string, len(keyHelp))
	for i, k := range keyHelp {
		keys[i] = fit(k[0], 14) + k[1]
	}
	lines := sideBySide(left, panel(m.Plain, "keys", keys, rw), lw, rw)

	pw := w - 49
	head := " " + "    " + fitRight("size", 9) + " " + fit("path", pw) + " " + fit("age", 5) + " " + fit("updatedAt", 10) + " " + fit("source", 16)
	lines = append(lines, style(m.Plain, "1", head))

	vis := m.visible()
	rowsH := h - len(lines) - 1
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
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	return append(lines, statusLine(m, vis, w))
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
	s := fmt.Sprintf("%d rows, sorted by %s", len(vis), sortNames[m.Sort])
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

// confirmLines draws the confirmation for the rows selected now. truncated is true when the window is too small to
// list every item (and every container id of a group): the list then ends with an "and N more" line and `y` is
// refused until the window is larger.
func confirmLines(m Model, w, h int) (lines []string, truncated bool) {
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
	lines = []string{fmt.Sprintf("Delete %d %s, %s in total?", len(paths), noun, sizeText(known, unknown))}
	if optOff > 0 {
		lines = append(lines, fmt.Sprintf("%d of them are optional and currently switched off.", optOff))
	}
	lines = append(lines, "")
	var entries []string
	for _, r := range rows {
		if len(r.Members) == 0 {
			entries = append(entries, "  "+fitRight(fmtBytes(r.Bytes), 9)+"  "+shorten(r.Path, w-13))
			continue
		}
		entries = append(entries, "  "+fitRight(fmtBytes(r.Bytes), 9)+"  "+shorten(r.Path, w-25)+fmt.Sprintf(" (%d containers)", len(r.Members)))
		for _, id := range r.Members {
			entries = append(entries, "      "+shorten(id, w-6))
		}
	}
	room := h - len(lines) - 1
	if len(entries) > room {
		truncated = true
		entries = append(entries[:max(room-1, 0)], fmt.Sprintf("  ... and %d more", len(entries)-max(room-1, 0)))
	}
	lines = append(lines, entries...)
	footer := "y/enter: delete  n/esc: cancel"
	if truncated {
		footer = "too many items for this window: enlarge it to confirm  n/esc: cancel"
	}
	return withFooter(lines, footer, w, h), truncated
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
