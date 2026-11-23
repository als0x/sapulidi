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
	// Containers lists the ids a group row stands for; the confirmation shows each one.
	Containers []string
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
	return strings.Contains(strings.ToLower(r.Path), f) ||
		strings.Contains(strings.ToLower(r.Category), f) ||
		strings.Contains(strings.ToLower(r.Source), f)
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

func sumBytes(rows []Row) int64 {
	var n int64
	for _, r := range rows {
		if r.Bytes > 0 {
			n += r.Bytes
		}
	}
	return n
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
