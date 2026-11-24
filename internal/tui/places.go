package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// placeOf is the place of an absolute path, in this order: the longest root that is a path-segment prefix of it
// (the root itself); else the longest base that is a path-segment prefix of it and not the path itself (a base
// holds the throw-away working folders of tools); else the parent directory. Home is shown as ~ and a place ends
// with "/". A path that is not absolute has no place here: "" (placeOfRow names it by its category).
func placeOf(path string, roots, bases []string, home string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	path = filepath.Clean(path)
	best := longestPrefix(path, roots, true)
	if best == "" {
		best = longestPrefix(path, bases, false)
	}
	if best == "" {
		best = filepath.Dir(path)
	}
	return showHome(best, home)
}

// longestPrefix is the longest absolute entry of dirs that is a path-segment prefix of path; with self the path
// itself counts too.
func longestPrefix(path string, dirs []string, self bool) string {
	best := ""
	for _, d := range dirs {
		d = strings.TrimRight(d, "/")
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		if ((self && path == d) || strings.HasPrefix(path, d+"/")) && len(d) > len(best) {
			best = d
		}
	}
	return best
}

// showHome abbreviates home to ~ and ends the place with "/".
func showHome(dir, home string) string {
	home = strings.TrimRight(home, "/")
	switch {
	case home != "" && dir == home:
		dir = "~"
	case home != "" && strings.HasPrefix(dir, home+"/"):
		dir = "~" + dir[len(home):]
	}
	return strings.TrimRight(dir, "/") + "/"
}

// placeOfRow is the place of a row: rows without a real path go to "commands · <category>".
func placeOfRow(r Row, roots, bases []string, home string) string {
	if p := placeOf(r.Path, roots, bases, home); p != "" {
		return p
	}
	return "commands · " + clean(r.Category)
}

// place is one line of the places view.
type place struct {
	Name                    string
	ReadyN, OptN, KeptN     int
	ExclN, OtherN           int // excluded rows; rows that are neither (wholesale commands)
	ReadyBytes, OptBytes    int64
	ReadyUnknown, OptUnknwn bool
	Updated                 time.Time
	Selectable, Selected    int
}

// readyRow reports a selectable row that is on by default (auto or optional switched on).
func readyRow(r Row) bool { return r.selectable() && r.Source != SrcOptionalOff }

// allPlaces groups every row by place, in no particular order.
func (m Model) allPlaces() []place {
	idx := map[string]*place{}
	var order []string
	for _, r := range m.Rows {
		p, ok := idx[r.Place]
		if !ok {
			p = &place{Name: r.Place}
			idx[r.Place] = p
			order = append(order, r.Place)
		}
		switch {
		case r.Source == SrcExcluded:
			p.ExclN++
		case r.Source == SrcKept:
			p.KeptN++
		case readyRow(r):
			p.ReadyN++
			addBytes(&p.ReadyBytes, &p.ReadyUnknown, r.Bytes)
		case r.selectable():
			p.OptN++
			addBytes(&p.OptBytes, &p.OptUnknwn, r.Bytes)
		default:
			p.OtherN++
		}
		if r.selectable() {
			p.Selectable++
			if m.Selected[r.Path] {
				p.Selected++
			}
		}
		if r.Updated.After(p.Updated) {
			p.Updated = r.Updated
		}
	}
	out := make([]place, 0, len(order))
	for _, n := range order {
		out = append(out, *idx[n])
	}
	return out
}

func addBytes(sum *int64, unknown *bool, b int64) {
	if b < 0 {
		*unknown = true
		return
	}
	*sum += b
}

// listed reports whether the places view shows the place: it has a ready, optional or excluded row (or a
// wholesale command row), or `g` is on.
func (p place) listed(showKept bool) bool {
	return p.ReadyN+p.OptN+p.ExclN+p.OtherN > 0 || showKept
}

func (p place) size() int64 { return p.ReadyBytes + p.OptBytes }

// places returns the places in view: filtered by the search and sorted.
func (m Model) places() []place {
	f := strings.ToLower(m.Filter)
	var out []place
	for _, p := range m.allPlaces() {
		if !p.listed(m.ShowKept) || f != "" && !strings.Contains(strings.ToLower(p.Name), f) {
			continue
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch m.Sort {
		case SortUpdated:
			if c := cmpTime(a.Updated, b.Updated); c != 0 {
				return c < 0
			}
		case SortPath:
		default:
			if a.size() != b.size() {
				return a.size() > b.size()
			}
		}
		return a.Name < b.Name
	})
	return out
}

// nextPlacesSort cycles size, updatedAt, path (the flat list's age sort has no meaning for a place).
func nextPlacesSort(k SortKey) SortKey {
	switch k {
	case SortSize, SortAge:
		return SortUpdated
	case SortUpdated:
		return SortPath
	}
	return SortSize
}

// placesSortName names the order of the places; the flat list's age sort shows as the size sort there.
func placesSortName(k SortKey) string {
	switch k {
	case SortUpdated:
		return sortNames[SortUpdated]
	case SortPath:
		return sortNames[SortPath]
	}
	return "ready + optional size, largest first"
}

// plural is "1 row", "2 rows".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// firstPlace is the index of the first place that has a selectable row, 0 when none has.
func firstPlace(m Model) int {
	for i, p := range m.places() {
		if p.Selectable > 0 {
			return i
		}
	}
	return 0
}

// keepPlaceOn puts the cursor on the place called name when it is still in view, else on the first selectable one.
func (m Model) keepPlaceOn(name string) Model {
	for i, p := range m.places() {
		if p.Name == name {
			m.Cursor = i
			return m
		}
	}
	m.Cursor = firstPlace(m)
	return m
}

func (m Model) cursorPlace() (place, bool) {
	v := m.places()
	if m.Cursor < 0 || m.Cursor >= len(v) {
		return place{}, false
	}
	return v[m.Cursor], true
}

func clampPlaceCursor(m Model, c int) int {
	return max(min(c, len(m.places())-1), 0)
}

// placesKey handles a key of the places screen.
func placesKey(m Model, k KeyMsg) (Model, []Effect) {
	if m.Filtering {
		return filterKey(m, k)
	}
	if m.PlacePrompt != "" {
		return promptKey(m, k)
	}
	if m.small() && (k.Special == KeyEnter || k.Rune == 'a' || k.Rune == ' ' || k.Rune == 'd') {
		return m, nil
	}
	cur, curOK := m.cursorPlace()
	switch {
	case k.Special == KeyUp || k.Rune == 'k':
		m.Cursor = clampPlaceCursor(m, m.Cursor-1)
	case k.Special == KeyDown || k.Rune == 'j':
		m.Cursor = clampPlaceCursor(m, m.Cursor+1)
	case k.Special == KeyEnter:
		if curOK {
			m.PlacesFilter, m.Filter = m.Filter, ""
			m.Screen, m.Place = ScreenPlace, cur.Name
			m.Cursor = firstSelectable(m)
		}
	case k.Special == KeyEsc:
		m.Filter = ""
		m.Cursor = firstPlace(m)
		if m.UnkState == UnkSearching { // esc also stops the second phase
			m.UnkState = UnkCancelled
			m.Status = "search for uncovered folders cancelled"
			return m, []Effect{CancelUncovered{}}
		}
	case k.Rune == 'q':
		return m, []Effect{Quit{}}
	case k.Rune == '/':
		m.Filtering = true
	case k.Rune == 'a':
		m = selectReadyIn(m, m.places())
	case k.Rune == ' ':
		if curOK {
			m = spacePlace(m, cur)
		}
	case k.Rune == 's':
		m.Sort = nextPlacesSort(m.Sort)
		m = m.keepPlaceOn(cur.Name)
	case k.Rune == 'g':
		m.ShowKept = !m.ShowKept
		m = m.keepPlaceOn(cur.Name)
	case k.Rune == 'o':
		m.Status = "optional rows are always listed here"
	case k.Rune == 'c':
		m = toggleView(m)
	case k.Rune == '?':
		m = openHelp(m)
	case k.Rune == 'u':
		m = openUnknown(m)
	case k.Rune == 'x':
		m.Status = "open the place to exclude a row"
	case k.Rune == 'd':
		return openConfirm(m)
	}
	return m, nil
}

// optionalPaths are the selectable optional-off rows of a place.
func (m Model) optionalPaths(name string) (paths []string, all bool) {
	all = true
	for _, r := range m.Rows {
		if r.Place == name && r.selectable() && !readyRow(r) {
			paths = append(paths, r.Path)
			all = all && m.Selected[r.Path]
		}
	}
	return paths, all
}

// unselectedOptional counts the optional rows of a place that are not selected, with their size.
func (m Model) unselectedOptional(name string) (n int, bytes int64, unknown bool) {
	paths, _ := m.optionalPaths(name)
	byPath := map[string]Row{}
	for _, r := range m.Rows {
		if r.Place == name {
			byPath[r.Path] = r
		}
	}
	for _, p := range paths {
		if !m.Selected[p] {
			n++
			addBytes(&bytes, &unknown, byPath[p].Bytes)
		}
	}
	return n, bytes, unknown
}

// spacePlace is `space` on a place. Ready rows are toggled directly; a place with only optional rows asks first,
// so an optional row is never selected without the user seeing the question.
func spacePlace(m Model, cur place) Model {
	if cur.ReadyN == 0 && cur.OptN > 0 {
		paths, all := m.optionalPaths(cur.Name)
		if !all {
			m.PlacePrompt = cur.Name
			return m
		}
		m.Selected = cloneSel(m.Selected)
		for _, p := range paths {
			delete(m.Selected, p)
		}
		return m
	}
	m = selectReadyIn(m, []place{cur})
	if cur.ReadyN > 0 && m.Status == "" && m.placeReadySelected(cur.Name) {
		n, b, u := m.unselectedOptional(cur.Name)
		switch {
		case n > 0:
			m.Status = fmt.Sprintf("%s selected; %s (%s) stay unselected: enter to pick them",
				plural(cur.ReadyN, "ready row"), plural(n, "optional row"), sizeCell(1, b, u))
		case cur.ReadyN > 1:
			m.Status = fmt.Sprintf("selected %d ready rows in %s (enter to see them)", cur.ReadyN, cur.Name)
		}
	}
	return m
}

// promptKey handles the optional-rows question: only y, n and esc do anything.
func promptKey(m Model, k KeyMsg) (Model, []Effect) {
	if m.small() { // the prompt is not on screen: no key acts
		m.PlacePrompt = ""
		return m, nil
	}
	if k.Rune != 'y' && k.Rune != 'n' && k.Special != KeyEsc {
		return m, nil
	}
	name := m.PlacePrompt
	m.PlacePrompt = ""
	if k.Rune != 'y' {
		return m, nil
	}
	paths, _ := m.optionalPaths(name)
	for _, p := range m.allPlaces() {
		if p.Name == name && p.ReadyN > 0 { // the rows changed under the prompt: ask again with space
			paths = nil
		}
	}
	m.Selected = cloneSel(m.Selected)
	for _, p := range paths {
		m.Selected[p] = true
	}
	return m, nil
}

// promptLine is the one-line question of the optional-rows prompt, fitted to w columns.
func promptLine(m Model, w int) string {
	n, b, u := 0, int64(0), false
	for _, p := range m.allPlaces() {
		if p.Name == m.PlacePrompt {
			n, b, u = p.OptN, p.OptBytes, p.OptUnknwn
		}
	}
	tail := fmt.Sprintf(")?  y: yes %s n / esc: no", dot(m.Plain))
	head := fmt.Sprintf("Select all %s in ", plural(n, "optional row"))
	size := " (" + sizeCell(1, b, u)
	room := w - len(head) - len(size) - len(tail)
	return head + shorten(placeText(m, m.PlacePrompt), max(room, 8)) + size + tail
}

// placeReadySelected reports whether every ready row of the place is selected.
func (m Model) placeReadySelected(name string) bool {
	for _, r := range m.Rows {
		if r.Place == name && readyRow(r) && !m.Selected[r.Path] {
			return false
		}
	}
	return true
}

// selectReadyIn toggles every READY row of the given places: when all of them are selected they are deselected.
// Optional-off and kept rows are never touched. With nothing ready it only says why.
func selectReadyIn(m Model, ps []place) Model {
	names := map[string]bool{}
	var optOnly, keptOnly, wholeOnly bool
	for _, p := range ps {
		names[p.Name] = true
		wholeOnly = wholeOnly || p.Selectable == 0 && p.OtherN > 0
		optOnly = optOnly || p.ReadyN == 0 && p.OptN > 0
		keptOnly = keptOnly || p.ReadyN == 0 && p.KeptN > 0
	}
	var ready []string
	all := true
	for _, r := range m.Rows {
		if names[r.Place] && readyRow(r) {
			ready = append(ready, r.Path)
			all = all && m.Selected[r.Path]
		}
	}
	if len(ready) == 0 {
		switch {
		case len(ps) != 1:
			m.Status = "nothing ready to select"
		case optOnly:
			m.Status = "only optional rows here: open it and pick them"
		case keptOnly, wholeOnly:
			m.Status = "protected: nothing here can be removed from this screen"
		default:
			m.Status = "nothing ready to select here"
		}
		return m
	}
	m.Selected = cloneSel(m.Selected)
	for _, p := range ready {
		if all {
			delete(m.Selected, p)
		} else {
			m.Selected[p] = true
		}
	}
	return m
}

// selectReadyVisible is `a` inside a place: it selects the ready rows in view, never the optional switched-off ones.
func selectReadyVisible(m Model) Model {
	m.Selected = cloneSel(m.Selected)
	n := 0
	for _, r := range m.visible() {
		if readyRow(r) {
			m.Selected[r.Path] = true
			n++
		}
	}
	if n == 0 {
		m.Status = "no ready rows here: space picks an optional row"
	}
	return m
}

// toggleView switches between the places and the flat list; the selection is not touched.
func toggleView(m Model) Model {
	m.Filter, m.PlacesFilter, m.Pinned = "", "", ""
	if m.Screen == ScreenList {
		m.Screen = ScreenPlaces
		m.Cursor = firstPlace(m)
		return m
	}
	m.Screen = ScreenList
	m.Cursor = firstSelectable(m)
	return m
}

// placeMark is the checkbox of a place: none, all, some of its selectable rows selected, or none selectable.
func placeMark(p place) string {
	switch {
	case p.Selectable == 0:
		return "[-]"
	case p.Selected == p.Selectable:
		return "[x]"
	case p.Selected > 0:
		return "[~]"
	}
	return "[ ]"
}

func sizeCell(n int, bytes int64, unknown bool) string {
	if n == 0 {
		return "-"
	}
	s := fmtBytes(bytes)
	if unknown {
		s += "+"
	}
	return s
}

// placeText is a place name as shown: sanitised, and ASCII only in plain mode.
func placeText(m Model, name string) string {
	name = clean(name)
	if m.Plain {
		name = strings.ReplaceAll(name, " · ", " | ")
	}
	return name
}

func placeRow(m Model, p place, cur bool, pw int) string {
	mark := " "
	if cur {
		mark = ">"
	}
	kept := "-"
	if p.KeptN > 0 {
		kept = fmt.Sprintf("%d", p.KeptN)
	}
	line := mark + placeMark(p) + " " + fitRight(sizeCell(p.ReadyN, p.ReadyBytes, p.ReadyUnknown), 9) + " " +
		fitRight(sizeCell(p.OptN, p.OptBytes, p.OptUnknwn), 9) + " " + fit(shorten(placeText(m, p.Name), pw), pw) + " " +
		fitRight(kept, 4) + " " + fit(fmtDate(p.Updated), 10)
	switch {
	case cur:
		return style(m.Plain, "7", line)
	case p.Selectable == 0:
		return style(m.Plain, "2", line)
	}
	return line
}

func placesHead(pw int) string {
	return " " + "    " + fitRight("ready", 9) + " " + fitRight("optional", 9) + " " + fit("place", pw) + " " +
		fitRight("kept", 4) + " " + fit("updatedAt", 10)
}

func viewPlaces(m Model, w, h int) []string {
	lines := topPanels(m, w)
	pw := w - 42
	lines = append(lines, style(m.Plain, "1", placesHead(pw)), cut("  "+legend(m, "ready"), w))
	if s := nothingReadyLine(m); s != "" {
		for _, l := range strings.Split(s, "\n") { // two lines: the whole sentence must fit 80 columns
			lines = append(lines, cut("  "+l, w))
		}
	}
	vis := m.places()
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
		lines = append(lines, placeRow(m, vis[i], i == m.Cursor, pw))
	}
	for len(lines) < h-2 {
		lines = append(lines, "")
	}
	return append(lines, placesStatus(m, vis, w), cut(footerOf(m), w))
}

// nothingReadyLine says so in plain words when no row is ready but some can be chosen by hand; "" otherwise.
func nothingReadyLine(m Model) string {
	var optN int
	var optB int64
	var optU bool
	for _, r := range m.Rows {
		switch {
		case readyRow(r):
			return ""
		case r.selectable():
			optN++
			addBytes(&optB, &optU, r.Bytes)
		}
	}
	if optN == 0 {
		return ""
	}
	return fmt.Sprintf("nothing is ready to delete automatically;\n%s can be chosen by hand: press enter on a place", sizeCell(1, optB, optU))
}

func placesStatus(m Model, vis []place, w int) string {
	switch {
	case m.PlacePrompt != "":
		return promptLine(m, w)
	case m.Filtering:
		return cut("/"+m.Filter+"_", w)
	case m.Summary.Paused:
		return style(m.Plain, "33", cut("paused: run sapulidi resume", w))
	case m.Status != "":
		return cut(m.Status, w)
	}
	if p, ok := m.cursorPlace(); ok {
		s := fmt.Sprintf("%d ready %s %d optional %s %d kept", p.ReadyN, dot(m.Plain), p.OptN, dot(m.Plain), p.KeptN)
		if p.ExclN > 0 {
			s += fmt.Sprintf(" %s %d excluded", dot(m.Plain), p.ExclN)
		}
		if p.ReadyN+p.OptN+p.ExclN+p.OtherN == 0 {
			return cut(s+": only kept rows, nothing can be removed. enter to see why", w)
		}
		return cut(s+". enter to look inside", w)
	}
	s := fmt.Sprintf("%s, sorted by %s", plural(len(vis), "place"), placesSortName(m.Sort))
	if m.Filter != "" {
		s = fmt.Sprintf("filter: %s (esc clears), %s", m.Filter, s)
	}
	return cut(s, w)
}
