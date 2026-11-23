package guard

import (
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/exclude"
)

// Excluded keeps a candidate the user excluded: its path (or a path below it, or one that contains an
// exclusion) matches the list. Base is checked for being inside an exclusion; for a ToolCommand the path
// the tool reports is Candidate.Path, which is checked like any other. A nil list excludes nothing.
func Excluded(list *exclude.List, c core.Candidate) error {
	if list == nil {
		return nil
	}
	if c.Path != "" {
		if m, ok := list.Match(c.Path); ok {
			return core.Keep("excluded", m)
		}
	}
	if c.Base != "" {
		if m, ok := list.Within(c.Base); ok {
			return core.Keep("excluded", m)
		}
	}
	return nil
}
