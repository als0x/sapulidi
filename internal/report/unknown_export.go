package report

import (
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
)

// ScanUncovered is the unknown-folder scan of `report` for a plan already made: the big folders that no rule of
// the summary covers, largest first, at most 20, with the report's 30 s budget. partial is true when the budget ran
// out or env.Ctx ended, so the list may be shorter and the sizes lower bounds. Paths are cleaned for display.
func ScanUncovered(env *core.Env, s engine.Summary) (dirs []DirUsage, partial bool) {
	dirs = ScanUnknown(env, coveredPaths(env, &s), ScanOptions{Budget: scanBudget, MinBytes: unknownMin, Max: maxUnknown})
	for i := range dirs {
		dirs[i].Path = Clean(dirs[i].Path)
		partial = partial || !dirs[i].SizeKnown
	}
	return dirs, partial || env.Ctx != nil && env.Ctx.Err() != nil
}
