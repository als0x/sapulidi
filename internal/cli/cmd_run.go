package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/confirm"
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
	recommended := fs.Bool("recommended", false, "list the recommended rows, ask, and remove them once (needs a terminal)")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}
	b, err := parseBudget(*budget)
	if err != nil {
		return usageFail(app, "sapulidi run: %v", err)
	}
	if *recommended && !confirm.StdioIsTerminal() {
		return refuseNoTerminal(app)
	}
	env, err := app.Build()
	if err != nil {
		return fail(app, "run", err)
	}
	st, err := OpenStore()
	if err != nil {
		return fail(app, "run", err)
	}
	opt := engine.Options{DryRun: *dry, Quick: *quick, Budget: b, Trigger: *trigger}
	if *recommended {
		plan := planFn(env, categories(), opt)
		// A path-less row cannot be named in Paths, so it is neither shown nor counted.
		var rows []core.Candidate
		for _, c := range withLabel(plan.WouldRemove, core.LabelRecommended) {
			if c.Path != "" {
				rows = append(rows, c)
				opt.Paths = append(opt.Paths, c.Path)
			}
		}
		if *dry {
			if !app.JSON && len(rows) > 0 {
				fmt.Fprint(app.Stdout, recommendedList(rows, len(rows)))
			}
			return printRun(app, engine.Summary{Mode: "dry-run", WouldRemove: rows, Kept: plan.Kept})
		}
		// An empty Paths would mean every row.
		if len(rows) == 0 {
			if !app.JSON {
				for _, f := range invalidConfigFiles() {
					fmt.Fprintln(app.Stdout, invalidConfigMessage(f))
				}
				fmt.Fprintln(app.Stdout, "no recommended rows to remove")
				return 0
			}
			return printRun(app, engine.Summary{Mode: "run"})
		}
		switch err := confirm.Terminal(recommendedPrompt(rows), "y"); {
		case errors.Is(err, confirm.ErrNoTerminal):
			return refuseNoTerminal(app)
		case errors.Is(err, confirm.ErrMismatch):
			return fail(app, "run", errors.New("not confirmed, nothing was removed"))
		case err != nil:
			return fail(app, "run", err)
		}
		// Only the rows just shown are removed, whatever else has consent or appears meanwhile.
		opt.IncludeRecommended = true
	}
	sum, err := engine.Run(env, categories(), st, opt)
	if err != nil {
		return fail(app, "run", err)
	}
	return printRun(app, sum)
}

func refuseNoTerminal(app *App) int {
	msg := "run this in your own terminal: sapulidi run --recommended"
	fmt.Fprintln(app.Stderr, msg)
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"error": msg})
	}
	return 1
}

// promptRows is how many recommended rows the question lists.
const promptRows = 10

// recommendedPrompt lists the first recommended rows and asks whether to remove them.
func recommendedPrompt(rows []core.Candidate) string {
	return recommendedList(rows, promptRows) + "Remove them now? [y/N] "
}

// recommendedList is the count and size of the recommended rows and at most limit of them.
func recommendedList(rows []core.Candidate, limit int) string {
	var s strings.Builder
	n, size := candidateTotal(rows)
	fmt.Fprintf(&s, "%s: %d item(s), %s\n", core.LabelRecommended.Words(), n, humanBytes(size))
	for i, c := range rows {
		if i == limit {
			fmt.Fprintf(&s, "  and %d more (sapulidi plan lists them all)\n", len(rows)-i)
			break
		}
		fmt.Fprintf(&s, "  %-17s %10s  %s\n", c.Category, humanBytes(c.Bytes), safeText(c.Path))
	}
	return s.String()
}

// printRun prints the outcome of a run and returns the exit code.
func printRun(app *App, sum engine.Summary) int {
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
	// A run that was held back (paused, busy, uninstalled, not set up yet) does not update either.
	if *trigger == "schedule" && !*dry && (sum.Mode == "run" || sum.Mode == "dry-run") {
		autoUpdate()
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
