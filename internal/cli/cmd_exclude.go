package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/exclude"
)

func init() {
	Register(Command{Name: "exclude", Summary: "never touch a path: 'exclude <path> [--note text]'; '--list'; '--remove <path>' (needs a terminal)", Run: runExclude})
}

const excludeUsage = "usage: sapulidi exclude <path> [--note text] | --list | --remove <path>"

func runExclude(app *App, args []string) int {
	var path, note string
	list, remove := false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--list":
			list = true
		case a == "--remove":
			remove = true
		case a == "--note":
			if i+1 >= len(args) {
				return usageFail(app, "%s", excludeUsage)
			}
			i++
			note = args[i]
		case path == "" && (a == "-" || !strings.HasPrefix(a, "-")):
			path = a
		default:
			return usageFail(app, "%s", excludeUsage)
		}
	}
	switch {
	case list && (remove || path != "" || note != ""):
		return usageFail(app, "%s", excludeUsage)
	case remove && (path == "" || note != ""):
		return usageFail(app, "%s", excludeUsage)
	case !list && path == "":
		return usageFail(app, "%s", excludeUsage)
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return fail(app, "exclude", err)
	}
	switch {
	case list:
		l, err := exclude.Load(paths)
		if err != nil {
			return fail(app, "exclude", err)
		}
		return listExclusions(app, l)
	case remove:
		return removeExclusion(app, paths, path)
	}
	var added bool
	var by, stored string
	err = exclude.Update(paths, func(l *exclude.List) error {
		var err error
		added, by, err = l.Add(path, note, time.Now())
		if err != nil || !added {
			return err
		}
		stored = l.Entries()[len(l.Entries())-1].Path
		return l.Save()
	})
	if err != nil {
		return fail(app, "exclude", err)
	}
	if !added {
		if app.JSON {
			writeJSON(app.Stdout, map[string]any{"added": false, "message": "already excluded", "by": by})
		} else {
			fmt.Fprintf(app.Stdout, "already excluded: %s\n", safeText(by))
		}
		return 0
	}
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"added": true, "path": stored})
	} else {
		fmt.Fprintf(app.Stdout, "excluded %s\n", safeText(stored))
	}
	return 0
}

func listExclusions(app *App, l *exclude.List) int {
	es := l.Entries()
	if app.JSON {
		if es == nil {
			es = []exclude.Entry{}
		}
		writeJSON(app.Stdout, map[string]any{"exclusions": es})
		return 0
	}
	if len(es) == 0 {
		fmt.Fprintln(app.Stdout, "no exclusions")
		return 0
	}
	fmt.Fprintf(app.Stdout, "%-10s  %-50s  %s\n", "ADDED", "PATH", "NOTE")
	for _, e := range es {
		fmt.Fprintf(app.Stdout, "%-10s  %-50s  %s\n", safeText(e.Added), safeText(e.Path), safeText(e.Note))
	}
	return 0
}

func removeExclusion(app *App, paths config.Paths, path string) int {
	// Terminal tier (S15): taking a protection away is the user's own act, retyped in a real terminal.
	err := confirm.Terminal("This lets sapulidi touch "+safeText(path)+" again.\nType remove to continue: ", "remove")
	switch {
	case errors.Is(err, confirm.ErrNoTerminal):
		msg := "run this in your own terminal: sapulidi exclude --remove " + safeText(path)
		fmt.Fprintln(app.Stderr, msg)
		if app.JSON {
			writeJSON(app.Stdout, map[string]any{"error": msg})
		}
		return 1
	case errors.Is(err, confirm.ErrMismatch):
		return fail(app, "exclude", errors.New("not confirmed, nothing changed"))
	case err != nil:
		return fail(app, "exclude", err)
	}
	// Load afresh under the lock only now: the confirmation can block for a long time.
	err = exclude.Update(paths, func(l *exclude.List) error {
		if err := l.Remove(path); err != nil {
			return err
		}
		return l.Save()
	})
	if err != nil {
		return fail(app, "exclude", errors.New(safeText(err.Error())))
	}
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"removed": path})
	} else {
		fmt.Fprintf(app.Stdout, "removed exclusion %s\n", safeText(path))
	}
	return 0
}
