package tui

import (
	"sort"
	"strings"
	"time"
)

// Row sources.
const (
	SrcAuto        = "auto"
	SrcOptionalOn  = "optional-on"
	SrcOptionalOff = "optional-off"
	SrcExcluded    = "excluded"
	SrcKept        = "kept"
)

// Row is one line of the list.
type Row struct {
	Path     string
	Bytes    int64 // -1 = unknown
	Created  time.Time
	Updated  time.Time
	Source   string // one of the Src constants
	Base     string // the source before the row was excluded; "" means auto
	Category string
	RuleKey  string
	Why      string   // why it is a candidate
	Block    string   // when set, the row cannot be selected and this is the reason
	Guards   []string // guards that passed, with their numbers
	Children []Child  // largest children
	// Members makes the row a group (stopped containers): the member candidate paths, `<engine>:<id>`. Selecting the
	// group selects every member and the confirmation lists each one; the group's own Path is never deleted.
	Members []string
}

// Child is one of the largest things inside a row.
type Child struct {
	Name  string
	Bytes int64
}

// WholesaleReason is the dim reason of a row for an official cache-cleaning command (spec 14.1).
func WholesaleReason(command string) string {
	return "cleaned by " + command + ", runs only when the disk is low"
}

const excludedReason = "excluded by you; x removes the exclusion"

func (r Row) origin() string {
	if r.Source != SrcExcluded {
		return r.Source
	}
	if r.Base == "" {
		return SrcAuto
	}
	return r.Base
}

// selectable reports whether space and `a` may select the row.
func (r Row) selectable() bool {
	switch r.Source {
	case SrcAuto, SrcOptionalOn, SrcOptionalOff:
		return r.Block == ""
	}
	return false
}

// reason is the one-line status for a row that cannot be selected, "" for a selectable row.
func (r Row) reason() string {
	switch {
	case r.Source == SrcExcluded:
		return excludedReason
	case r.Source == SrcKept && r.Block == "":
		return "kept by a guard"
	}
	return r.Block
}

// visible returns the rows the list shows, filtered and sorted.
func (m Model) visible() []Row {
	out := make([]Row, 0, len(m.Rows))
	f := strings.ToLower(m.Filter)
	for _, r := range m.Rows {
		if r.origin() == SrcOptionalOff && !m.ShowOptional {
			continue
		}
		if f != "" && !matches(r, f) {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return less(m.Sort, out[i], out[j]) })
	return out
}

func matches(r Row, f string) bool {
	for _, s := range []string{r.Path, r.Category, r.Source, sourceLabel(r.Source, true), sourceLabel(r.Source, false)} {
		if strings.Contains(strings.ToLower(s), f) {
			return true
		}
	}
	return false
}

// deletePaths are the paths Delete carries for rows: a group's members, otherwise the row's path; no path twice.
func deletePaths(rows []Row) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range rows {
		ps := r.Members
		if len(ps) == 0 {
			ps = []string{r.Path}
		}
		for _, p := range ps {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// dedupeRows keeps the first row of each path.
func dedupeRows(rows []Row) []Row {
	seen := map[string]bool{}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if !seen[r.Path] {
			seen[r.Path] = true
			out = append(out, r)
		}
	}
	return out
}

func less(k SortKey, a, b Row) bool {
	switch k {
	case SortSize:
		if a.Bytes != b.Bytes {
			return a.Bytes > b.Bytes
		}
	case SortAge:
		if c := cmpTime(a.Created, b.Created); c != 0 {
			return c < 0
		}
	case SortUpdated:
		if c := cmpTime(a.Updated, b.Updated); c != 0 {
			return c < 0
		}
	}
	return a.Path < b.Path
}

// cmpTime orders oldest first with an unknown (zero) time last.
func cmpTime(a, b time.Time) int {
	switch {
	case a.IsZero() && b.IsZero():
		return 0
	case a.IsZero():
		return 1
	case b.IsZero():
		return -1
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	}
	return 0
}

// selected returns the selected rows that are in view, in list order.
func (m Model) selected() []Row {
	var out []Row
	for _, r := range m.visible() {
		if m.Selected[r.Path] && r.selectable() {
			out = append(out, r)
		}
	}
	return out
}

// sumBytes adds the known sizes and counts the rows of unknown size.
func sumBytes(rows []Row) (known int64, unknown int) {
	for _, r := range rows {
		if r.Bytes < 0 {
			unknown++
		} else {
			known += r.Bytes
		}
	}
	return known, unknown
}

func (m Model) cursorRow() (Row, bool) {
	v := m.visible()
	if m.Cursor < 0 || m.Cursor >= len(v) {
		return Row{}, false
	}
	return v[m.Cursor], true
}

func cloneSel(s map[string]bool) map[string]bool {
	out := make(map[string]bool, len(s)+1)
	for k, v := range s {
		out[k] = v
	}
	return out
}

// sourceLabel is the text of the source column.
func sourceLabel(src string, plain bool) string {
	switch src {
	case SrcOptionalOn:
		if plain {
			return "optional-on"
		}
		return "optional · on"
	case SrcOptionalOff:
		if plain {
			return "optional-off"
		}
		return "optional · off"
	}
	return src
}
