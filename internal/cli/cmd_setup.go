package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/schedule"
)

func init() {
	Register(Command{Name: "setup", Summary: "show roots, categories and a dry run; --yes records your consent " +
		"(--recommended: recommended rows too)", Run: runSetup})
}

const recommendedQuestion = "Also let the schedule remove recommended rows? [y/N] "

type catSummaryJSON struct {
	Category string `json:"category"`
	Bucket   string `json:"bucket"`
	Enabled  bool   `json:"enabled"`
	Count    int    `json:"count"`
	Bytes    int64  `json:"bytes"`
}

func runSetup(app *App, args []string) int {
	fs := newFlags(app, "setup")
	yes := fs.Bool("yes", false, "record consent for every safe rule shown")
	recommended := fs.Bool("recommended", false, "with --yes: also record consent for the recommended rules")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}
	if *recommended && !*yes {
		return usageFail(app, "usage: sapulidi setup [--yes [--recommended]]")
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

	cats := categories()
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
	safeCount := len(keys)
	recKeys := recommendedRuleKeys(env.Rules)

	out := app.Stdout
	if !app.JSON {
		_, skipped, err := discoverRoots()
		if err != nil {
			return fail(app, "setup", err)
		}
		for _, f := range invalid {
			fmt.Fprintln(out, invalidConfigMessage(f))
		}
		printSetupRoots(out, env.Roots, skipped)
		printSetupCategoriesOff(out, rows)
		fmt.Fprintln(out, "Plan (a dry run, nothing was removed):")
		printSetupPlan(out, env.Rules, sum.WouldRemove)
		total, totalBytes := candidateTotal(sum.WouldRemove)
		fmt.Fprintf(out, "  total: %d item(s), %s; %s: %d\n", total, humanBytes(totalBytes),
			core.LabelProtected.Words(), len(sum.Kept))
	}

	recorded, withRecommended := 0, false
	if *yes {
		withRecommended = *recommended
		if withRecommended && len(recKeys) > 0 {
			if code := confirmAllow(app, "setup", recKeys, "setup --yes --recommended"); code != 0 {
				return code
			}
			// The question can wait a long time, and another process may have recorded consent meanwhile.
			if code, done := refuseExistingConsent(app); done {
				return code
			}
		}
		// The default is no: only a confirmed flag, or "y" typed on a real terminal, widens the record.
		if !withRecommended && !app.JSON && len(recKeys) > 0 {
			withRecommended = confirm.Terminal(setupRecommendedPrompt(sum.WouldRemove), "y") == nil
			// The question can wait a long time, and another process may have recorded consent meanwhile.
			if code, done := refuseExistingConsent(app); done {
				return code
			}
		}
		if withRecommended {
			keys = append(keys, recKeys...)
			slices.Sort(keys)
		}
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

	if *yes {
		fmt.Fprintf(out, "consent recorded for %d rules\n", recorded)
		if !withRecommended && len(recKeys) > 0 {
			fmt.Fprintln(out, "To let the schedule remove recommended rows later: sapulidi consent --yes --recommended")
		}
	} else {
		fmt.Fprintf(out, "Nothing is recorded yet. To allow the %d safe rule(s) to run, confirm with: sapulidi setup --yes\n", safeCount)
	}
	fmt.Fprintf(out, "Schedule: %s\n", sched)
	fmt.Fprintln(out, "Next: sapulidi schedule install")
	return 0
}

// recommendedRuleKeys returns the sorted keys of every recommended rule.
func recommendedRuleKeys(rules []core.Rule) []string {
	var keys []string
	for _, r := range rules {
		if r.Bucket == core.Optional && r.Recommend {
			keys = append(keys, r.Key())
		}
	}
	slices.Sort(keys)
	return keys
}

// setupRecommendedPrompt formats the interactive prompt for recommended rules, including count and size when non-empty.
func setupRecommendedPrompt(cands []core.Candidate) string {
	n, b := candidateTotal(withLabel(cands, core.LabelRecommended))
	if n == 0 {
		return recommendedQuestion
	}
	return fmt.Sprintf("Also let the schedule remove recommended rows (%d item(s), %s)? [y/N] ", n, humanBytes(b))
}

// printSetupCategoriesOff prints a compact line only when any non-never category is switched off.
func printSetupCategoriesOff(out io.Writer, rows []catSummaryJSON) {
	var off []string
	for _, r := range rows {
		if r.Bucket != string(core.Never) && !r.Enabled {
			off = append(off, safeText(r.Category))
		}
	}
	if len(off) > 0 {
		fmt.Fprintf(out, "Categories off: %s\n", strings.Join(off, ", "))
	}
}

// printSetupRoots summarizes the roots found and the folders discovery skipped. The sources come from discovery and
// are sanitized before printing.
func printSetupRoots(out io.Writer, found []core.Root, skipped []core.Kept) {
	var skippedCount int
	var budgetHit bool
	for _, k := range skipped {
		switch k.Why {
		case "no-codex": // not a folder
		case "budget":
			budgetHit = true
		default:
			skippedCount++
		}
	}
	var b strings.Builder
	b.WriteString("Roots: ")
	if len(found) == 0 {
		b.WriteString("none found")
	} else {
		fmt.Fprintf(&b, "%d repo(s)", len(found))
		srcSet := map[string]struct{}{}
		for _, r := range found {
			if s := safeText(r.Source); s != "" {
				srcSet[s] = struct{}{}
			}
		}
		if len(srcSet) > 0 {
			sources := slices.Sorted(maps.Keys(srcSet))
			fmt.Fprintf(&b, " (%s)", strings.Join(sources, ", "))
		}
	}
	if skippedCount > 0 {
		fmt.Fprintf(&b, " · %d skipped (run 'sapulidi roots' for the full list)", skippedCount)
	}
	fmt.Fprintln(out, b.String())
	if budgetHit {
		fmt.Fprintln(out, "  "+budgetNote)
	}
}

// agentName is the agent whose folders a rule cleans, or "".
func agentName(r core.Rule) string {
	switch {
	case strings.HasPrefix(r.Location, "claude-"):
		return "Claude Code"
	case strings.HasPrefix(r.Location, "codex-"):
		return "Codex"
	}
	return ""
}

// printSetupPlan sums the plan by label and, inside a label, by agent for agent rules and by category otherwise.
func printSetupPlan(out io.Writer, rules []core.Rule, cands []core.Candidate) {
	agents := map[string]string{}
	for _, r := range rules {
		agents[r.Key()] = agentName(r)
	}
	for _, l := range removableLabels {
		group := withLabel(cands, l)
		if len(group) == 0 {
			continue
		}
		n, b := candidateTotal(group)
		fmt.Fprintf(out, "  %s: %d item(s), %s\n", l.Words(), n, humanBytes(b))
		parts := map[string][]core.Candidate{}
		for _, c := range group {
			name := agents[c.RuleKey]
			if name == "" {
				name = c.Category
			}
			parts[name] = append(parts[name], c)
		}
		for _, name := range slices.Sorted(maps.Keys(parts)) {
			n, b := candidateTotal(parts[name])
			fmt.Fprintf(out, "    %-18s %d item(s), %s\n", name, n, humanBytes(b))
		}
	}
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
