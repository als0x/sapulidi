package cli

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/state"
)

func init() {
	Register(Command{Name: "consent", Summary: "list the rules waiting for your OK; --yes [--recommended | <rule>...] " +
		"confirms them; '--revoke <rule>|recommended' (needs a terminal)", Run: runConsent})
}

// consentRules loads the rules of the packs; tests replace it.
var consentRules = loadRules

const consentUsage = "usage: sapulidi consent [--yes [--recommended] | --yes <rule-key>...] | --revoke <rule-key>|recommended"

func runConsent(app *App, args []string) int {
	fs := newFlags(app, "consent")
	yes := fs.Bool("yes", false, "confirm the waiting safe rules")
	recommended := fs.Bool("recommended", false, "with --yes: also confirm the waiting recommended rules")
	revoke := fs.String("revoke", "", "take consent back from a rule key, or from all recommended rules")
	// Rule keys are the only positional arguments, so parseFlags (which refuses them) is not used.
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return 0
	} else if err != nil {
		return usageFail(app, consentUsage)
	}
	named := fs.Args()
	revoking := false
	fs.Visit(func(f *flag.Flag) { revoking = revoking || f.Name == "revoke" })
	badNamed := len(named) > 0 && (!*yes || *recommended ||
		slices.ContainsFunc(named, func(a string) bool { return strings.HasPrefix(a, "-") }))
	if badNamed || (*recommended && !*yes) || (revoking && (*yes || *revoke == "")) {
		return usageFail(app, consentUsage)
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
	cfg, _, err := config.Load(paths.Config)
	if err != nil {
		return fail(app, "consent", err)
	}
	// A corrupt config trusts no consent, so none is changed and nothing is offered.
	if len(cfg.Invalid) > 0 {
		msg := invalidConfigMessage(cfg.Invalid[0])
		if *yes || revoking {
			return fail(app, "consent", errors.New(msg))
		}
		if app.JSON {
			writeJSON(app.Stdout, map[string]any{"waiting": []string{}, "labels": map[string]core.Label{},
				"recorded": false, "confirmed": []string{}, "invalid_config": cfg.Invalid})
		} else {
			fmt.Fprintln(app.Stdout, msg)
		}
		return 0
	}
	rules, _ := consentRules(paths)
	if revoking {
		return revokeConsent(app, st, rules, *revoke)
	}
	waiting := waitingRules(rules, consent, cfg)

	for _, k := range named {
		if !slices.ContainsFunc(waiting, func(w waitingRule) bool { return w.key == k }) {
			return fail(app, "consent", fmt.Errorf("%s is not waiting for your OK; nothing was recorded", safeText(k)))
		}
	}
	var confirmed, yourCall []string
	var left []waitingRule
	for _, w := range waiting {
		var take bool
		switch {
		case !*yes:
		case len(named) > 0:
			take = slices.Contains(named, w.key)
		default:
			take = w.label == core.LabelSafe || *recommended && w.label == core.LabelRecommended
		}
		if !take {
			left = append(left, w)
			continue
		}
		confirmed = append(confirmed, w.key)
		if w.label == core.LabelYourCall {
			yourCall = append(yourCall, w.key)
		}
	}
	if len(yourCall) > 0 {
		// Terminal tier: only the user, in a real terminal, widens what the schedule removes beyond what
		// sapulidi stands behind.
		prompt := "This lets scheduled runs remove what these rules find:\n"
		for _, k := range yourCall {
			prompt += "  " + safeText(k) + "\n"
		}
		retry := "--yes " + safeText(strings.Join(named, " "))
		if code := confirmConsent(app, prompt+"Type allow to continue: ", "allow", retry); code != 0 {
			return code
		}
	}
	if len(confirmed) > 0 {
		// Read afresh: the confirmation can block for a long time.
		now, err := st.Consent()
		if err != nil || now == nil {
			return fail(app, "consent", errors.Join(errors.New("cannot read the consent record"), err))
		}
		keys := append(slices.Clone(now.Rules), confirmed...)
		slices.Sort(keys)
		if err := st.SaveConsent(&config.Consent{At: time.Now().UTC(), Rules: slices.Compact(keys)}); err != nil {
			return fail(app, "consent", err)
		}
		st.Logger().Event("consent", map[string]any{"rules": confirmed})
	}
	if app.JSON {
		// waiting lists what waited when the command started, the rules --yes confirmed included.
		keys, labels := []string{}, map[string]core.Label{}
		for _, w := range waiting {
			keys = append(keys, w.key)
			labels[w.key] = w.label
		}
		writeJSON(app.Stdout, map[string]any{"waiting": keys, "labels": labels, "recorded": len(confirmed) > 0,
			"confirmed": nonNil(confirmed)})
		return 0
	}
	if len(confirmed) > 0 {
		fmt.Fprintf(app.Stdout, "consent recorded for %d rules\n", len(confirmed))
	}
	printWaiting(app, left)
	return 0
}

type waitingRule struct {
	key   string
	label core.Label
}

// waitingRules lists, sorted by key, the rules without consent: every safe and recommended rule, and every "your
// call" rule of a category the config did not switch off.
func waitingRules(rules []core.Rule, consent *config.Consent, cfg *config.Config) []waitingRule {
	var out []waitingRule
	for _, r := range rules {
		if r.Bucket == core.Never || slices.Contains(consent.Rules, r.Key()) {
			continue
		}
		label := core.LabelYourCall
		switch {
		case r.Bucket == core.Auto:
			label = core.LabelSafe
		case r.Recommend:
			label = core.LabelRecommended
		}
		en := cfg.Categories[r.Category].Enabled
		if label == core.LabelYourCall && en != nil && !*en {
			continue
		}
		out = append(out, waitingRule{r.Key(), label})
	}
	slices.SortFunc(out, func(a, b waitingRule) int { return cmp.Compare(a.key, b.key) })
	return out
}

// printWaiting prints the rules that still wait: first those --yes can confirm, then the "your call" group.
func printWaiting(app *App, left []waitingRule) {
	var main, yourCall []waitingRule
	count := map[core.Label]int{}
	for _, w := range left {
		count[w.label]++
		if w.label == core.LabelYourCall {
			yourCall = append(yourCall, w)
		} else {
			main = append(main, w)
		}
	}
	list := func(rules []waitingRule) {
		for _, w := range rules {
			fmt.Fprintf(app.Stdout, "  %s  %s\n", w.key, w.label.Words())
		}
	}
	switch {
	case len(main) == 0:
		fmt.Fprintln(app.Stdout, "no rules are waiting for your OK")
	case count[core.LabelSafe] == 0:
		fmt.Fprintln(app.Stdout, "no rules are waiting for your OK as safe")
	}
	if len(main) > 0 {
		fmt.Fprintf(app.Stdout, "%d rule(s) are not allowed in scheduled runs:\n", len(main))
		list(main)
	}
	if count[core.LabelSafe] > 0 {
		fmt.Fprintln(app.Stdout, "to confirm the safe rules, run: sapulidi consent --yes")
	}
	if count[core.LabelRecommended] > 0 {
		fmt.Fprintln(app.Stdout, "to also confirm the recommended rules, run: sapulidi consent --yes --recommended")
	}
	if len(yourCall) > 0 {
		fmt.Fprintln(app.Stdout, "your call rules (scheduled runs leave them alone unless you allow one by its key):")
		list(yourCall)
		fmt.Fprintln(app.Stdout, "to allow one, run in your terminal: sapulidi consent --yes <rule-key>")
	}
}

// confirmConsent is the terminal tier (S15) of a consent change: the user types word on the terminal. It returns 0
// when confirmed, else the exit code after saying why; retry is how to run the command again in a terminal.
func confirmConsent(app *App, prompt, word, retry string) int {
	err := confirm.Terminal(prompt, word)
	switch {
	case errors.Is(err, confirm.ErrNoTerminal):
		msg := "run this in your own terminal: sapulidi consent " + retry
		fmt.Fprintln(app.Stderr, msg)
		if app.JSON {
			writeJSON(app.Stdout, map[string]any{"error": msg})
		}
		return 1
	case errors.Is(err, confirm.ErrMismatch):
		return fail(app, "consent", errors.New("not confirmed, nothing changed"))
	case err != nil:
		return fail(app, "consent", err)
	}
	return 0
}

// revokeConsent removes one rule key, or every recommended one, from the consent record.
func revokeConsent(app *App, st *state.Store, rules []core.Rule, what string) int {
	drop := map[string]bool{what: true}
	if what == "recommended" {
		drop = map[string]bool{}
		for _, r := range rules {
			if r.Recommend {
				drop[r.Key()] = true
			}
		}
	}
	// split returns the consented keys that stay and those that go.
	split := func() (kept, gone []string, err error) {
		consent, err := st.Consent()
		if err != nil || consent == nil {
			return nil, nil, errors.Join(errors.New("cannot read the consent record"), err)
		}
		kept = []string{}
		for _, k := range consent.Rules {
			if drop[k] {
				gone = append(gone, k)
			} else {
				kept = append(kept, k)
			}
		}
		if len(gone) == 0 {
			return nil, nil, fmt.Errorf("nothing to revoke: no consent recorded for %s", safeText(what))
		}
		return kept, gone, nil
	}
	if _, _, err := split(); err != nil {
		return fail(app, "consent", err)
	}
	// Terminal tier (S15), like exclude --remove: changing the consent record is the user's own act.
	prompt := "This takes your OK back from " + safeText(what) + ".\nType revoke to continue: "
	if code := confirmConsent(app, prompt, "revoke", "--revoke "+safeText(what)); code != 0 {
		return code
	}
	// Read afresh only now: the confirmation can block for a long time.
	kept, gone, err := split()
	if err != nil {
		return fail(app, "consent", err)
	}
	if err := st.SaveConsent(&config.Consent{At: time.Now().UTC(), Rules: kept}); err != nil {
		return fail(app, "consent", err)
	}
	st.Logger().Event("consent-revoked", map[string]any{"rules": gone})
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"revoked": gone})
	} else {
		fmt.Fprintf(app.Stdout, "consent revoked for %d rules\n", len(gone))
	}
	return 0
}
