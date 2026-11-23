package cli

import (
	"fmt"

	"github.com/als0x/sapulidi/internal/config"
)

func init() {
	Register(Command{Name: "config", Summary: "show the effective config; 'path'; 'set <key> <value>'", Run: runConfig})
}

const configUsage = "usage: sapulidi config [path | set <key> <value>]"

func runConfig(app *App, args []string) int {
	paths, err := config.ResolvePaths()
	if err != nil {
		return fail(app, "config", err)
	}
	switch {
	case len(args) == 0:
		return showConfig(app, paths)
	case args[0] == "path" && len(args) == 1:
		if app.JSON {
			writeJSON(app.Stdout, map[string]any{"config": paths.Config, "applied": config.AppliedPath(paths.Config)})
		} else {
			fmt.Fprintln(app.Stdout, paths.Config)
		}
		return 0
	case args[0] == "set" && len(args) == 3:
		// config.Apply validates the key and the value; a never category or an age below the minimum is refused.
		if err := config.Apply(paths, config.Change{Key: args[1], Value: args[2]}); err != nil {
			return fail(app, "config set", err)
		}
		if app.JSON {
			writeJSON(app.Stdout, map[string]any{"key": args[1], "value": args[2], "file": config.AppliedPath(paths.Config)})
		} else {
			fmt.Fprintf(app.Stdout, "set %s = %s (in %s)\n", args[1], args[2], config.AppliedPath(paths.Config))
		}
		return 0
	}
	return usageFail(app, "%s", configUsage)
}

func showConfig(app *App, paths config.Paths) int {
	cfg, warns, err := config.Load(paths.Config)
	if err != nil {
		return fail(app, "config", err)
	}
	vals := cfg.Effective()
	if app.JSON {
		type row struct {
			Key    string `json:"key"`
			Value  string `json:"value"`
			Source string `json:"source"`
		}
		rows := make([]row, 0, len(vals))
		for _, v := range vals {
			rows = append(rows, row{v.Key, v.Value, v.Source})
		}
		if warns == nil {
			warns = []string{}
		}
		writeJSON(app.Stdout, map[string]any{"config": rows, "warnings": warns})
		return 0
	}
	for _, w := range warns {
		fmt.Fprintln(app.Stdout, "note: "+safeText(w))
	}
	for _, v := range vals {
		fmt.Fprintf(app.Stdout, "%-32s %-12s (%s)\n", safeText(v.Key), safeText(v.Value), v.Source)
	}
	return 0
}
