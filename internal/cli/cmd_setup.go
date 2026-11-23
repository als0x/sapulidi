package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/schedule"
)

func init() {
	Register(Command{Name: "setup", Summary: "show roots, categories and a dry run; --yes records your consent", Run: runSetup})
}

type catSummaryJSON struct {
	Category string `json:"category"`
	Bucket   string `json:"bucket"`
	Enabled  bool   `json:"enabled"`
	Count    int    `json:"count"`
	Bytes    int64  `json:"bytes"`
}

func runSetup(app *App, args []string) int {
	fs := newFlags(app, "setup")
	yes := fs.Bool("yes", false, "record consent for every auto rule shown")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}
	invalid := invalidConfigFiles()
	if *yes {
		// Refuse before anything is scanned or recorded: consent on a config nobody could read is no consent,
		// and a second setup --yes must not widen an existing record with rules the user has not reviewed.
		if len(invalid) > 0 {
			return fail(app, "setup", errors.New(invalidConfigMessage(invalid[0])+"; nothing was recorded"))
		}
		if code, done := refuseExistingConsent(app); done {
			return code
		}
	}
	env, sum, ok := planOf(app, "setup")
	if !ok {
		return 1
	}

	cats := core.Categories()
	sort.Slice(cats, func(i, j int) bool { return cats[i].Name() < cats[j].Name() })
	rows := make([]catSummaryJSON, 0, len(cats))
	for _, c := range cats {
		var mine []core.Candidate
		for _, w := range sum.WouldRemove {
			if w.Category == c.Name() {
				mine = append(mine, w)
			}
		}
		n, b := candidateTotal(mine)
		rows = append(rows, catSummaryJSON{c.Name(), string(c.Bucket()), env.Settings.CategoryEnabled(c.Name()), n, b})
	}

	keys := autoRuleKeys(env.Rules)
	recorded := 0
	if *yes {
		st, err := OpenStore()
		if err != nil {
			return fail(app, "setup", err)
		}
		if err := st.SaveConsent(&config.Consent{At: time.Now().UTC(), Rules: keys}); err != nil {
			return fail(app, "setup", err)
		}
		st.Logger().Event("consent", map[string]any{"rules": keys, "by": "setup"})
		recorded = len(keys)
	}
	sched := scheduleStatusText()

	if app.JSON {
		type rootJSON struct {
			Path   string `json:"path"`
			Source string `json:"source"`
		}
		roots := make([]rootJSON, 0, len(env.Roots))
		for _, r := range env.Roots {
			roots = append(roots, rootJSON{r.Path, r.Source})
		}
		writeJSON(app.Stdout, map[string]any{
			"roots": roots, "categories": rows, "consent_recorded": recorded, "rules": nonNil(keys),
			"schedule": sched, "invalid_config": nonNil(invalid),
		})
		return 0
	}

	out := app.Stdout
	for _, f := range invalid {
		fmt.Fprintln(out, invalidConfigMessage(f))
	}
	fmt.Fprintln(out, "Roots:")
	if len(env.Roots) == 0 {
		fmt.Fprintln(out, "  none found (add some with roots = [...] in the config file)")
	}
	for _, r := range env.Roots {
		fmt.Fprintf(out, "  %s (%s)\n", r.Path, r.Source)
	}
	fmt.Fprintln(out, "Categories:")
	for _, r := range rows {
		state := "off"
		if r.Enabled {
			state = "on"
		}
		fmt.Fprintf(out, "  %-18s %-9s %s\n", r.Category, r.Bucket, state)
	}
	fmt.Fprintln(out, "Plan (a dry run, nothing was removed):")
	total, totalBytes := candidateTotal(sum.WouldRemove)
	for _, r := range rows {
		if r.Count > 0 {
			fmt.Fprintf(out, "  %-18s %d item(s), %s\n", r.Category, r.Count, humanBytes(r.Bytes))
		}
	}
	fmt.Fprintf(out, "  total: %d item(s), %s; kept: %d\n", total, humanBytes(totalBytes), len(sum.Kept))
	if *yes {
		fmt.Fprintf(out, "consent recorded for %d rules\n", recorded)
	} else {
		fmt.Fprintf(out, "Nothing is recorded yet. To allow the %d auto rule(s) above to run, confirm with: sapulidi setup --yes\n", len(keys))
	}
	fmt.Fprintf(out, "Schedule: %s\n", sched)
	fmt.Fprintln(out, "Next: sapulidi schedule install")
	return 0
}

// refuseExistingConsent fails (done is true) when a consent record already exists; first-time setup passes.
func refuseExistingConsent(app *App) (code int, done bool) {
	st, err := OpenStore()
	if err != nil {
		return fail(app, "setup", err), true
	}
	consent, err := st.Consent()
	if err != nil || consent == nil {
		return 0, false // none, or an unreadable one that setup may replace
	}
	waiting := 0
	if paths, err := config.ResolvePaths(); err == nil {
		rules, _ := loadRules(paths)
		waiting = len(waitingRuleKeys(rules, consent))
	}
	return fail(app, "setup", fmt.Errorf("consent already recorded; run sapulidi consent to review the %d waiting rules", waiting)), true
}

// scheduleStatusText says whether the scheduled run is installed. It queries the real scheduler, so the test
// flag skips it.
func scheduleStatusText() string {
	if os.Getenv("SAPULIDI_TEST") == "1" {
		return "not checked (test mode)"
	}
	be, paths, err := scheduleSetup()
	if err != nil {
		return "unavailable (" + err.Error() + ")"
	}
	for _, j := range schedule.Jobs(paths) {
		if j.Label != "dev.sapulidi.run" {
			continue
		}
		st, err := be.Status(j.Label)
		switch {
		case err != nil:
			return "unknown (" + err.Error() + ")"
		case st.Installed:
			return "installed (" + be.Name() + ")"
		}
		return "not installed (" + be.Name() + ")"
	}
	return "not installed"
}
