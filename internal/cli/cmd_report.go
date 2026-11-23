package cli

import (
	"fmt"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/report"
)

func init() {
	Register(Command{Name: "report", Summary: "what uses the disk: categories, models, unknown big directories, growth", Run: runReport})
}

func partial(known bool) string {
	if known {
		return ""
	}
	return " (partial)"
}

func runReport(app *App, args []string) int {
	fs := newFlags(app, "report")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}
	env, err := app.Build()
	if err != nil {
		return fail(app, "report", err)
	}
	st, _ := OpenStore() // without a store there is no growth, the rest still works
	r, err := report.Build(env, core.Categories(), st)
	if err != nil {
		return fail(app, "report", err)
	}
	if app.JSON {
		writeJSON(app.Stdout, r)
		return 0
	}
	out := app.Stdout
	fmt.Fprintf(out, "disk: %s free of %s\n", humanBytes(int64(r.Free)), humanBytes(int64(r.Total)))
	fmt.Fprintf(out, "%-18s %-9s %-4s %18s %18s\n", "category", "bucket", "on", "would remove", "kept")
	for _, c := range r.Categories {
		on := "off"
		if c.Enabled {
			on = "on"
		}
		fmt.Fprintf(out, "%-18s %-9s %-4s %18s %18s\n", c.Category, c.Bucket, on,
			fmt.Sprintf("%d, %s", c.Candidates, humanBytes(c.CandidateBytes)),
			fmt.Sprintf("%d, %s", c.Kept, humanBytes(c.KeptBytes)))
	}
	if len(r.Models) > 0 {
		fmt.Fprintln(out, "\nmodels (never removed):")
		for _, m := range r.Models {
			last := ""
			if m.LastUsed != "" {
				last = ", last used " + m.LastUsed
			}
			fmt.Fprintf(out, "  %-12s %-32s %12s%s%s\n", m.Tool, m.Name, humanBytes(m.Bytes), partial(m.SizeKnown), last)
		}
	}
	if len(r.Unknown) > 0 {
		fmt.Fprintln(out, "\nbig directories no rule knows:")
		for _, d := range r.Unknown {
			fmt.Fprintf(out, "  %12s%s  %s\n", humanBytes(d.Bytes), partial(d.SizeKnown), d.Path)
		}
	}
	if len(r.Personal) > 0 {
		fmt.Fprintln(out, "\npersonal folders (never scanned, total only):")
		for _, d := range r.Personal {
			fmt.Fprintf(out, "  %12s%s  %s\n", humanBytes(d.Bytes), partial(d.SizeKnown), d.Path)
		}
	}
	if len(r.Growth) > 0 {
		fmt.Fprintln(out, "\ngrowing in the last 7 days:")
		for _, g := range r.Growth {
			fmt.Fprintf(out, "  +%11s  %s (now %s)\n", humanBytes(g.GrowthBytes), g.Path, humanBytes(g.Bytes))
		}
	}
	for _, a := range r.Anomalies {
		fmt.Fprintf(out, "anomaly: %s %s %s\n", a.Kind, a.Path, humanBytes(a.Bytes))
	}
	return 0
}
