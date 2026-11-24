package tui

import "slices"

// Update applies one message. It is pure: the input model is never changed.
func Update(m Model, msg Msg) (Model, []Effect) {
	switch msg := msg.(type) {
	case KeyMsg:
		return updateKey(m, msg)
	case ResizeMsg:
		m.W, m.H = msg.Width, msg.Height
		return m, nil
	case ScanProgressMsg:
		return scanProgress(m, msg), nil
	case ScanDoneMsg:
		if m.Screen == ScreenResult && m.Result.Pending {
			return m, nil // a deletion is running; its result comes first
		}
		if m.Screen == ScreenConfirm {
			m.Status = "the list changed: confirm again"
		}
		m.Rows = dedupeRows(msg.Rows)
		m.Summary = msg.Summary
		m.Screen = ScreenList
		m.Confirm = nil
		m.Cursor = 0
		sel := map[string]bool{}
		for _, r := range m.Rows {
			if m.Selected[r.Path] && r.selectable() {
				sel[r.Path] = true
			}
		}
		m.Selected = sel
		return m, nil
	case DeleteDoneMsg:
		return deleteDone(m, msg.Result), nil
	case ExclusionChangedMsg:
		return exclusionChanged(m, msg), nil
	}
	return m, nil
}

func scanProgress(m Model, p ScanProgressMsg) Model {
	m.Scan = slices.Clone(m.Scan)
	for i := range m.Scan {
		if m.Scan[i].Name == p.Category {
			m.Scan[i].Done, m.Scan[i].Total = p.Done, p.Total
			return m
		}
	}
	m.Scan = append(m.Scan, scanLine{Name: p.Category, Done: p.Done, Total: p.Total})
	return m
}

func deleteDone(m Model, r Result) Model {
	gone := map[string]bool{}
	for _, p := range r.Removed {
		gone[p] = true
	}
	rows := make([]Row, 0, len(m.Rows))
	m.Selected = cloneSel(m.Selected)
	for _, row := range m.Rows {
		if len(row.Members) > 0 {
			var left []string
			for _, p := range row.Members {
				if !gone[p] {
					left = append(left, p)
				}
			}
			if len(left) < len(row.Members) {
				delete(m.Selected, row.Path)
			}
			if len(left) > 0 {
				row.Members = left
				rows = append(rows, row)
			}
			continue
		}
		if gone[row.Path] {
			delete(m.Selected, row.Path)
			continue
		}
		rows = append(rows, row)
	}
	m.Rows = rows
	r.Pending = false
	m.Result = r
	m.Screen = ScreenResult
	m.Confirm = nil
	m.Cursor = clampCursor(m, m.Cursor)
	return m
}

func exclusionChanged(m Model, c ExclusionChangedMsg) Model {
	if m.Screen == ScreenConfirm {
		m.Screen = ScreenList
		m.Confirm = nil
		m.Status = "an exclusion changed: confirm again"
	}
	m.Rows = slices.Clone(m.Rows)
	for i, r := range m.Rows {
		if r.Path != c.Path {
			if c.Excluded && slices.Contains(r.Members, c.Path) {
				m.Selected = cloneSel(m.Selected) // a member of a group is excluded: the group is no longer selected
				delete(m.Selected, r.Path)
			}
			continue
		}
		if c.Excluded && r.Source != SrcExcluded {
			m.Rows[i].Base = r.Source
			m.Rows[i].Source = SrcExcluded
			m.Selected = cloneSel(m.Selected)
			delete(m.Selected, r.Path)
		} else if !c.Excluded && r.Source == SrcExcluded {
			m.Rows[i].Source = r.origin()
			m.Rows[i].Base = ""
			m.Rows[i].Origin = ""
		}
	}
	return m
}

func clampCursor(m Model, c int) int {
	n := len(m.visible())
	if c >= n {
		c = n - 1
	}
	return max(c, 0)
}

func updateKey(m Model, k KeyMsg) (Model, []Effect) {
	if k.Ctrl && k.Rune == 'c' {
		return m, []Effect{Quit{}}
	}
	m.Status = ""
	switch m.Screen {
	case ScreenScanning:
		if k.Special == KeyEsc {
			m.Screen = ScreenList
			m.Status = "scan cancelled"
			return m, []Effect{CancelScan{}}
		}
	case ScreenList:
		return listKey(m, k)
	case ScreenDetail:
		return detailKey(m, k)
	case ScreenConfirm:
		return confirmKey(m, k)
	case ScreenResult:
		if m.Result.Pending {
			return m, nil
		}
		if k.Special == KeyEsc || k.Special == KeyEnter || k.Rune == 'q' {
			m.Screen = ScreenList
		}
	}
	return m, nil
}

func listKey(m Model, k KeyMsg) (Model, []Effect) {
	if m.Filtering {
		return filterKey(m, k)
	}
	if m.small() && (k.Special == KeyEnter || k.Rune == 'a') {
		return m, nil
	}
	switch {
	case k.Special == KeyUp || k.Rune == 'k':
		m.Cursor = clampCursor(m, m.Cursor-1)
	case k.Special == KeyDown || k.Rune == 'j':
		m.Cursor = clampCursor(m, m.Cursor+1)
	case k.Special == KeyEnter:
		if _, ok := m.cursorRow(); ok {
			m.Screen = ScreenDetail
		}
	case k.Special == KeyEsc:
		m.Filter = ""
		m.Cursor = 0
	case k.Rune == 'q':
		return m, []Effect{Quit{}}
	case k.Rune == '/':
		m.Filtering = true
	case k.Rune == 'a':
		m = selectAll(m)
	case k.Rune == 's':
		m.Sort = (m.Sort + 1) % 4
	case k.Rune == 'o':
		m = toggleOptional(m)
	default:
		return rowAction(m, k)
	}
	return m, nil
}

// rowAction handles the keys that act on the row under the cursor (list and detail).
func rowAction(m Model, k KeyMsg) (Model, []Effect) {
	if m.small() {
		return m, nil
	}
	switch k.Rune {
	case ' ':
		if r, ok := m.cursorRow(); ok && r.selectable() {
			m.Selected = cloneSel(m.Selected)
			if m.Selected[r.Path] {
				delete(m.Selected, r.Path)
			} else {
				m.Selected[r.Path] = true
			}
		}
	case 'x':
		r, ok := m.cursorRow()
		switch {
		case !ok:
		case r.Source == SrcExcluded:
			return m, []Effect{RemoveExclusion{Path: r.Path}}
		case r.Source == SrcKept:
			m.Status = "kept by a guard: nothing to exclude"
		case len(r.Members) > 0:
			m.Status = "a group cannot be excluded as a whole"
		default:
			return m, []Effect{AddExclusion{Path: r.Path}}
		}
	case 'd':
		return openConfirm(m)
	}
	return m, nil
}

func openConfirm(m Model) (Model, []Effect) {
	if m.Summary.Paused {
		m.Status = "paused: run sapulidi resume"
		return m, nil
	}
	sel := m.selected()
	if len(sel) == 0 {
		m.Status = "nothing selected: space selects a row"
		return m, nil
	}
	m.Confirm = deletePaths(sel)
	m.Screen = ScreenConfirm
	return m, nil
}

// small reports that the window is known to be under 80x24; the keys that select or delete then do nothing.
func (m Model) small() bool { return m.W != 0 && (m.W < minWidth || m.H < minHeight) }

// size is the window size, or 80x24 before the first ResizeMsg.
func (m Model) size() (int, int) {
	if m.W == 0 {
		return minWidth, minHeight
	}
	return m.W, m.H
}

func selectAll(m Model) Model {
	m.Selected = cloneSel(m.Selected)
	for _, r := range m.visible() {
		if r.selectable() {
			m.Selected[r.Path] = true
		}
	}
	return m
}

func toggleOptional(m Model) Model {
	m.ShowOptional = !m.ShowOptional
	if !m.ShowOptional {
		m.Selected = cloneSel(m.Selected)
		for _, r := range m.Rows {
			if r.origin() == SrcOptionalOff {
				delete(m.Selected, r.Path)
			}
		}
	}
	m.Cursor = clampCursor(m, m.Cursor)
	return m
}

func filterKey(m Model, k KeyMsg) (Model, []Effect) {
	switch {
	case k.Special == KeyEsc:
		m.Filter, m.Filtering = "", false
	case k.Special == KeyEnter:
		m.Filtering = false
	case k.Special == KeyBackspace:
		if r := []rune(m.Filter); len(r) > 0 {
			m.Filter = string(r[:len(r)-1])
		}
	case k.Rune != 0 && !k.Ctrl && k.Special == "":
		m.Filter += string(k.Rune)
	default:
		return m, nil
	}
	m.Cursor = 0
	return m, nil
}

func detailKey(m Model, k KeyMsg) (Model, []Effect) {
	if k.Special == KeyEsc || k.Special == KeyEnter || k.Rune == 'q' {
		m.Screen = ScreenList
		return m, nil
	}
	m, eff := rowAction(m, k)
	if m.Screen == ScreenDetail && len(eff) == 0 {
		return m, nil
	}
	return m, eff
}

func confirmKey(m Model, k KeyMsg) (Model, []Effect) {
	switch {
	case k.Rune == 'y' || k.Special == KeyEnter:
		if m.small() {
			return m, nil
		}
		if w, h := m.size(); !slices.Equal(m.Confirm, deletePaths(m.selected())) || m.Summary.Paused {
			m.Screen, m.Confirm = ScreenList, nil
			m.Status = "the selection changed: nothing deleted"
			return m, nil
		} else if _, truncated := confirmLines(m, w, h); truncated {
			return m, nil // the list does not fit: enlarge the window
		}
		paths := slices.Clone(m.Confirm)
		m.Screen = ScreenResult
		m.Result = Result{Pending: true}
		return m, []Effect{Delete{Paths: paths}}
	case k.Rune == 'n' || k.Special == KeyEsc || k.Rune == 'q':
		m.Screen = ScreenList
		m.Confirm = nil
	}
	return m, nil
}
