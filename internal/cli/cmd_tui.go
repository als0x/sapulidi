package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/als0x/sapulidi"
	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/exclude"
	"github.com/als0x/sapulidi/internal/install"
	"github.com/als0x/sapulidi/internal/schedule"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/internal/tui"
)

func init() { TUI = runTUI }

// runTUI opens the interactive screen with the engine behind it.
func runTUI(app *App) int {
	st, err := OpenStore()
	if err != nil {
		return fail(app, "tui", err)
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return fail(app, "tui", err)
	}
	cats := core.Categories()
	return runTUIWith(app, &tui.EngineDeps{
		Build: app.Build,
		Cats:  cats,
		Run: func(env *core.Env, opt engine.Options) (engine.Summary, error) {
			return engine.Run(env, cats, st, opt)
		},
		Facts: func() tui.Facts { return tuiFacts(st) },
		Exclude: func(p string) error {
			if err := excludableTUIPath(p); err != nil {
				return err
			}
			return exclude.Update(paths, func(l *exclude.List) error {
				if _, _, err := l.Add(p, "", time.Now()); err != nil {
					return err
				}
				return l.Save()
			})
		},
		Unexclude: func(p string) error {
			return exclude.Update(paths, func(l *exclude.List) error {
				if err := l.Remove(p); err != nil {
					return err
				}
				return l.Save()
			})
		},
	})
}

// excludableTUIPath refuses what is not a plain absolute path: a command text would be stored relative to the
// working directory, and a `*` would turn the exclusion into a glob.
func excludableTUIPath(p string) error {
	switch {
	case !filepath.IsAbs(p):
		return errors.New("not an absolute path")
	case strings.Contains(p, "*"):
		return errors.New("a path with * would become a glob")
	}
	return nil
}

// runTUIWith runs the loop on deps and turns its end into an exit code.
func runTUIWith(app *App, deps tui.Deps) int {
	err := tui.Run(context.Background(), deps)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, tui.ErrNoTerminal):
		return runHelp(app, nil) // defensive: the front already sends a non-terminal to the help
	}
	return fail(app, "tui", err)
}

// tuiNotice is the update notice from the cache, or ""; it makes no request and starts no check.
func tuiNotice() string {
	paths, err := config.ResolvePaths()
	if err != nil {
		return ""
	}
	cfg, _, err := config.Load(paths.Config)
	if err != nil || !updateCheckEnabled(cfg) {
		return ""
	}
	return updateNotice(paths.State, install.Marketplace)
}

// tuiFacts reads what the summary panel shows besides the scan; whatever is unavailable stays zero.
func tuiFacts(st *state.Store) tui.Facts {
	f := tui.Facts{Paused: st.Paused(), Notice: tuiNotice()}
	if home, err := os.UserHomeDir(); err == nil {
		if p, err := config.ResolvePaths(); err == nil {
			env := &install.Env{Home: home, Data: p.Data, Plugin: sapulidi.Plugin}
			if rows, err := env.List(); err == nil {
				for _, r := range rows {
					if r.Installed {
						f.Agents++
					}
				}
			}
		}
	}
	if be, paths, err := scheduleSetup(); err == nil {
		for _, j := range schedule.Jobs(paths) {
			if j.Label != schedule.LabelRun {
				continue
			}
			if s, err := be.Status(j.Label); err == nil && s.Installed {
				f.Interval, f.NextIn = j.Interval, s.Next
			}
		}
	}
	return f
}
