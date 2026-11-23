package tui

import "fmt"

const (
	minWidth  = 80
	minHeight = 24
)

var sortNames = [...]string{"size, largest first", "age, oldest first", "updatedAt, stalest first", "path"}

// View renders the model into at most height lines of at most width columns each (ANSI codes not counted).
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
		lines = viewConfirm(m, width, height)
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
	msgs := []string{"make the window larger", fmt.Sprintf("(need %dx%d, have %dx%d)", minWidth, minHeight, w, h)}
	for i, s := range msgs {
		r := []rune(s)
		if len(r) > w {
			r = r[:w]
		}
		msgs[i] = string(r)
	}
	return msgs
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
	out := []string{head + repeat(h, w-1-len([]rune(head))) + tr}
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
		bw = max(bw, len([]rune(l)))
	}
	left := repeat(" ", max((w-bw)/2, 0))
	out := make([]string, 0, h)
	for i := 0; i < (h-len(block))/2; i++ {
		out = append(out, "")
	}
	for _, l := range block {
		out = append(out, left+l)
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
	{"o: optional", "q: quit"},
}

func summaryLines(m Model, iw int) []string {
	var excluded, kept, selectable int
	for _, r := range m.Rows {
		switch r.Source {
		case SrcExcluded:
			excluded++
		case SrcKept:
			kept++
		}
	}
	for _, r := range m.visible() {
		if r.selectable() {
			selectable++
		}
	}
	sel := m.selected()
	sched := "schedule: off"
	if m.Summary.Interval > 0 {
		sched = "schedule: every " + fmtInterval(m.Summary.Interval)
		if !m.Summary.Next.IsZero() {
			sched += ", next " + m.Summary.Next.Format("15:04")
		}
	}
	disk := "disk free: -"
	if m.Summary.FreeNow > 0 {
		disk = "disk free: " + fmtBytes(int64(m.Summary.FreeNow)) + " " + arrow(m.Plain) + " " +
			fmtBytes(int64(m.Summary.FreeNow)+sumBytes(sel))
	}
	return []string{
		fmt.Sprintf("agents installed: %d", m.Summary.Agents),
		fmt.Sprintf("to remove: %s, %d of %d selected", fmtBytes(sumBytes(sel)), len(sel), selectable),
		fmt.Sprintf("excluded: %d   kept by guards: %d", excluded, kept),
		sched,
		disk,
	}
}

func viewList(m Model, w, h int) []string {
	rw := max(32, w/3)
	lw := w - rw - 1
	left := panel(m.Plain, "summary", summaryLines(m, lw-4), lw)
	keys := make([]string, len(keyHelp))
	for i, k := range keyHelp {
		keys[i] = fit(k[0], 14) + k[1]
	}
	lines := sideBySide(left, panel(m.Plain, "keys", keys, rw), lw, rw)

	pw := w - 47
	head := " " + "    " + fitRight("size", 9) + " " + fit("path", pw) + " " + fit("age", 5) + " " + fit("updatedAt", 10) + " " + fit("source", 14)
	lines = append(lines, style(m.Plain, "1", head))

	vis := m.visible()
	rowsH := h - len(lines) - 1
	top := min(max(m.Cursor-rowsH/2, 0), max(len(vis)-rowsH, 0))
	if len(vis) == 0 {
		lines = append(lines, "  nothing to show (o: optional)")
	}
	for i := top; i < len(vis) && i < top+rowsH; i++ {
		lines = append(lines, listRow(m, vis[i], i == m.Cursor, pw))
	}
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	return append(lines, statusLine(m, vis))
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
		fit(sourceLabel(r.Source, m.Plain), 14)
	switch {
	case cur:
		return style(m.Plain, "7", line)
	case !r.selectable():
		return style(m.Plain, "2", line)
	}
	return line
}

func statusLine(m Model, vis []Row) string {
	switch {
	case m.Filtering:
		return "/" + m.Filter + "_"
	case m.Summary.Paused:
		return style(m.Plain, "33", "paused: run sapulidi resume")
	case m.Status != "":
		return m.Status
	}
	if r, ok := m.cursorRow(); ok && !r.selectable() {
		return r.reason()
	}
	s := fmt.Sprintf("%d rows, sorted by %s", len(vis), sortNames[m.Sort])
	if m.Filter != "" {
		s = fmt.Sprintf("filter: %s (esc clears), %s", m.Filter, s)
	}
	return s
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
	add("source", sourceLabel(r.Source, m.Plain))
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
	return withFooter(lines, "esc: back  space: select  x: exclude  d: delete", h)
}

func dot(plain bool) string {
	if plain {
		return "|"
	}
	return "·"
}

// withFooter cuts lines so that the footer stays on the last row.
func withFooter(lines []string, footer string, h int) []string {
	if len(lines) > h-1 {
		lines = lines[:h-1]
	}
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	return append(lines, footer)
}

func viewConfirm(m Model, w, h int) []string {
	rows := make([]Row, 0, len(m.Confirm))
	byPath := map[string]Row{}
	for _, r := range m.Rows {
		byPath[r.Path] = r
	}
	var optOff int
	for _, p := range m.Confirm {
		r := byPath[p]
		rows = append(rows, r)
		if r.origin() == SrcOptionalOff {
			optOff++
		}
	}
	noun := "items"
	if len(rows) == 1 {
		noun = "item"
	}
	lines := []string{fmt.Sprintf("Delete %d %s, %s in total?", len(rows), noun, fmtBytes(sumBytes(rows)))}
	if optOff > 0 {
		lines = append(lines, fmt.Sprintf("%d of them are optional and currently switched off.", optOff))
	}
	lines = append(lines, "")
	shown := max(min(len(rows), h-len(lines)-6), 1)
	for _, r := range rows[:shown] {
		lines = append(lines, "  "+fitRight(fmtBytes(r.Bytes), 9)+"  "+shorten(r.Path, w-13))
	}
	if len(rows) > shown {
		lines = append(lines, fmt.Sprintf("  ... and %d more", len(rows)-shown))
	}
	for _, r := range rows {
		for _, id := range r.Containers {
			lines = append(lines, "  container "+shorten(id, w-12))
		}
	}
	return withFooter(lines, "y/enter: delete  n/esc: cancel", h)
}

func viewResult(m Model, w, h int) []string {
	if m.Result.Pending {
		return withFooter([]string{"deleting..."}, "", h)
	}
	r := m.Result
	lines := []string{fmt.Sprintf("Freed %s, removed %d items.", fmtBytes(r.Freed), len(r.Removed))}
	if r.Err != "" {
		lines = append(lines, "error: "+shorten(r.Err, w-7))
	}
	if len(r.Kept) > 0 {
		lines = append(lines, "", fmt.Sprintf("Kept by a guard (%d):", len(r.Kept)))
		for _, k := range r.Kept {
			lines = append(lines, "  "+shorten(k.Path, w-4-len([]rune(k.Why)))+"  "+k.Why)
		}
	}
	return withFooter(lines, "esc: back", h)
}
