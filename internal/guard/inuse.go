package guard

import "github.com/als0x/sapulidi/internal/core"

// InUse is spec S6: an open file or process cwd under path, or (checkArgs) a live process that
// mentions path. A failing check counts as in use.
func InUse(p core.Platform, path string, checkArgs bool) error {
	open, err := p.IsOpen(path)
	if err != nil {
		return core.Keep("open", "check failed: "+err.Error())
	}
	if open {
		return core.Keep("open", path)
	}
	if checkArgs {
		m, err := p.ArgsMention(path)
		if err != nil {
			return core.Keep("process", "check failed: "+err.Error())
		}
		if m {
			return core.Keep("process", path)
		}
	}
	return nil
}
