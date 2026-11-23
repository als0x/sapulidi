package cli

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/als0x/sapulidi/internal/config"
)

func init() {
	Register(Command{Name: "consent", Summary: "list the auto rules waiting for your OK; --yes confirms them", Run: runConsent})
}

func runConsent(app *App, args []string) int {
	fs := newFlags(app, "consent")
	yes := fs.Bool("yes", false, "confirm the listed rules")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return fail(app, "consent", err)
	}
	st, err := OpenStore()
	if err != nil {
		return fail(app, "consent", err)
	}
	consent, err := st.Consent()
	if err != nil {
		return fail(app, "consent", err)
	}
	if consent == nil {
		return fail(app, "consent", errors.New("not set up yet: run sapulidi setup first"))
	}
	rules, _ := loadRules(paths)
	waiting := waitingRuleKeys(rules, consent)

	recorded := false
	if *yes && len(waiting) > 0 {
		keys := append(slices.Clone(consent.Rules), waiting...)
		slices.Sort(keys)
		if err := st.SaveConsent(&config.Consent{At: time.Now().UTC(), Rules: keys}); err != nil {
			return fail(app, "consent", err)
		}
		st.Logger().Event("consent", map[string]any{"rules": waiting})
		recorded = true
	}
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"waiting": nonNil(waiting), "recorded": recorded})
		return 0
	}
	switch {
	case recorded:
		fmt.Fprintf(app.Stdout, "consent recorded for %d rules\n", len(waiting))
	case len(waiting) == 0:
		fmt.Fprintln(app.Stdout, "no rules are waiting for your OK")
	default:
		fmt.Fprintf(app.Stdout, "%d rule(s) wait for your OK:\n", len(waiting))
		for _, k := range waiting {
			fmt.Fprintf(app.Stdout, "  %s\n", k)
		}
		fmt.Fprintln(app.Stdout, "to confirm them, run: sapulidi consent --yes")
	}
	return 0
}
