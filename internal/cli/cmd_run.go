package cli

import (
	"fmt"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
)

func init() {
	Register(Command{Name: "run", Summary: "remove what the rules and your consent allow", Run: runRun})
}

// parseBudget accepts "90m", "3h", "2d" (config style) and Go durations such as "45s".
func parseBudget(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	if d, err := config.ParseDuration(s); err == nil {
		return d.D(), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid budget %q", s)
	}
	return d, nil
}

func runRun(app *App, args []string) int {
	fs := newFlags(app, "run")
	quick := fs.Bool("quick", false, "skip slow categories and sizes (for hooks)")
	dry := fs.Bool("dry-run", false, "remove nothing")
	trigger := fs.String("trigger", "manual", "who started this run, for the log")
	budget := fs.String("budget", "", "stop after this long, e.g. 90s or 5m")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}
	b, err := parseBudget(*budget)
	if err != nil {
		return usageFail(app, "sapulidi run: %v", err)
	}
	env, err := app.Build()
	if err != nil {
		return fail(app, "run", err)
	}
	st, err := OpenStore()
	if err != nil {
		return fail(app, "run", err)
	}
	sum, err := engine.Run(env, core.Categories(), st, engine.Options{
		DryRun: *dry, Quick: *quick, Budget: b, Trigger: *trigger,
	})
	if err != nil {
		return fail(app, "run", err)
	}
	invalid := invalidConfigFiles()
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{
			"invalid_config": nonNil(invalid),
			"mode":           sum.Mode, "removed": len(sum.Removed), "would_remove": len(sum.WouldRemove),
			"kept": len(sum.Kept), "failed": len(sum.Failed), "freed_bytes": sum.FreedBytes,
			"stopped": sum.Stopped, "free_before": sum.FreeBefore, "free_after": sum.FreeAfter,
			"waiting": nonNil(sum.Waiting),
		})
	} else {
		for _, f := range invalid {
			fmt.Fprintln(app.Stdout, invalidConfigMessage(f))
		}
		fmt.Fprintln(app.Stdout, summaryLine(sum))
		for _, f := range sum.Failed {
			fmt.Fprintf(app.Stderr, "failed: %s %s\n", f.Category, f.Path)
		}
	}
	if len(sum.Failed) > 0 {
		return 1
	}
	return 0
}

func summaryLine(s engine.Summary) string {
	_, would := candidateTotal(s.WouldRemove)
	switch s.Mode {
	case "paused":
		return "paused: nothing ran (sapulidi resume lifts the pause)"
	case "busy":
		return "another run is in progress: nothing ran"
	case "no-consent":
		return fmt.Sprintf("no consent recorded yet: dry run, %d would be removed (%s), kept %d; run sapulidi setup",
			len(s.WouldRemove), humanBytes(would), len(s.Kept))
	case "run":
		line := fmt.Sprintf("removed %d (%s freed), kept %d, failed %d",
			len(s.Removed), humanBytes(s.FreedBytes), len(s.Kept), len(s.Failed))
		if len(s.WouldRemove) > 0 {
			line += fmt.Sprintf(", %d waiting for your OK", len(s.WouldRemove))
		}
		if s.Stopped {
			line += ", stopped at the time budget"
		}
		return line
	}
	return fmt.Sprintf("%s: dry run, %d would be removed (%s), kept %d, failed %d",
		s.Mode, len(s.WouldRemove), humanBytes(would), len(s.Kept), len(s.Failed))
}
