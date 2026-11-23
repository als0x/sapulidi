package guard

import (
	"path/filepath"

	"github.com/als0x/sapulidi/internal/core"
)

// Excluded keeps a candidate the user excluded: its path (or a path below it, or one that contains an
// exclusion) matches the list. Base is checked for being inside an exclusion; for a ToolCommand the path
// the tool reports is Candidate.Path, which is checked like any other.
//
// Dir (the main repository of a WorktreeRemove, the working dir of a ToolCommand) is checked too: a path-less
// ToolCommand (worktree prune) acts on the whole of Dir, so it is kept when Dir is excluded, below an exclusion or
// contains one; otherwise Dir only has to not sit inside an exclusion. A nil list excludes nothing.
func Excluded(list core.Excluder, c core.Candidate) error {
	if list == nil {
		return nil
	}
	usesDir := c.Dir != "" && (c.Removal == core.WorktreeRemove || c.Removal == core.ToolCommand)
	for _, p := range []string{c.Path, c.Base} {
		if p != "" && !filepath.IsAbs(p) {
			return core.Keep("excluded", "path is not absolute: "+p)
		}
	}
	if usesDir && !filepath.IsAbs(c.Dir) {
		return core.Keep("excluded", "path is not absolute: "+c.Dir)
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
	if usesDir {
		find := list.Within
		if c.Path == "" {
			find = list.Match
		}
		if m, ok := find(c.Dir); ok {
			return core.Keep("excluded", m)
		}
	}
	return nil
}
