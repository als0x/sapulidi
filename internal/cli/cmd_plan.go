package cli

import (
	"fmt"
)

func init() {
	Register(Command{Name: "plan", Summary: "show what a run would remove and what it keeps (dry run)", Run: runPlan})
}

func runPlan(app *App, args []string) int {
	fs := newFlags(app, "plan")
	all := fs.Bool("all", false, "list every kept item, one per line")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}
	_, sum, ok := planOf(app, "plan")
	if !ok {
		return 1
	}
	invalid := invalidConfigFiles()
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{
			"candidates":     candidatesJSON(sum.WouldRemove),
			"kept":           keptsJSON(sum.Kept),
			"waiting":        nonNil(sum.Waiting),
			"free_bytes":     sum.FreeBefore,
			"invalid_config": nonNil(invalid),
		})
		return 0
	}
	out := app.Stdout
	for _, f := range invalid {
		fmt.Fprintln(out, invalidConfigMessage(f))
	}
	n, total := candidateTotal(sum.WouldRemove)
	if n == 0 {
		fmt.Fprintln(out, "nothing to remove")
	}
	for _, c := range sum.WouldRemove {
		fmt.Fprintf(out, "%-12s %10s  %s\n", c.Category, humanBytes(c.Bytes), c.Path)
		fmt.Fprintf(out, "    %s\n", c.Reason)
	}
	if n > 0 {
		fmt.Fprintf(out, "total: %d candidates, %s\n", n, humanBytes(total))
	}
	if len(sum.Kept) > 0 {
		counts, whys := keptByWhy(sum.Kept)
		for _, w := range whys {
			fmt.Fprintf(out, "kept (%s): %d\n", w, counts[w])
		}
		if *all {
			for _, k := range sum.Kept {
				fmt.Fprintf(out, "  kept %-10s %-12s %s\n", k.Why, k.Category, k.Path)
			}
		}
	}
	if len(sum.Waiting) > 0 {
		fmt.Fprintf(out, "%d rule(s) wait for your OK: run sapulidi consent\n", len(sum.Waiting))
	}
	return 0
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
