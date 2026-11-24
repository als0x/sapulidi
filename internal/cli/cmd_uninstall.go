package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/als0x/sapulidi"
	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/delivery"
	"github.com/als0x/sapulidi/internal/install"
	"github.com/als0x/sapulidi/internal/schedule"
)

func nonNilResults(r []install.Result) []install.Result {
	if r == nil {
		return []install.Result{}
	}
	return r
}

func init() {
	Register(Command{Name: "uninstall", Summary: "remove the schedule and binaries; --all also config and state (needs a terminal)", Run: runUninstall})
}

func runUninstall(app *App, args []string) int {
	all := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "--all":
		all = true
	default:
		return usageFail(app, "usage: sapulidi uninstall [--all]")
	}
	be, paths, err := scheduleSetup()
	if err != nil {
		return fail(app, "uninstall", err)
	}
	home, _ := os.UserHomeDir()
	if all {
		// Terminal tier (S15): config and state hold the user's choices and consent, so the user retypes the word.
		err := confirm.Terminal("This removes the schedule, the binaries, the config and the state.\nType uninstall to continue: ", "uninstall")
		switch {
		case errors.Is(err, confirm.ErrNoTerminal):
			msg := "run this in your own terminal: sapulidi uninstall --all"
			fmt.Fprintln(app.Stderr, msg)
			if app.JSON {
				writeJSON(app.Stdout, map[string]any{"error": msg})
			}
			return 1
		case errors.Is(err, confirm.ErrMismatch):
			return fail(app, "uninstall", errors.New("not confirmed, nothing removed"))
		case err != nil:
			return fail(app, "uninstall", err)
		}
	}
	// What `--agents` installed goes first: the registered plugin's hooks run <data>/plugin/bin/sapulidi.
	env := &install.Env{Home: home, Data: paths.Data, Plugin: sapulidi.Plugin}
	if !app.JSON {
		env.Log = func(s string) { fmt.Fprintln(app.Stdout, s) }
	}
	agents, aerr := env.Remove("all")
	agentsFailed := aerr != nil
	for _, r := range agents {
		if r.Failed() {
			agentsFailed = true
		}
	}
	var res delivery.UninstallResult
	err = nil
	if agentsFailed {
		// Keep <data>: the record and the extracted folder let a rerun finish. The schedule still goes.
		for _, j := range schedule.Jobs(paths) {
			if rerr := be.Remove(j.Label); rerr != nil {
				err = fmt.Errorf("schedule remove %s: %w", j.Label, rerr)
				break
			}
			res.Schedule = append(res.Schedule, j.Label)
		}
		msg := "removing what agents installed failed, so nothing else was removed; fix that, then run: sapulidi agents remove all, and sapulidi uninstall again"
		if aerr != nil {
			msg += " (" + aerr.Error() + ")"
		}
		for _, r := range agents {
			for _, n := range r.Notes {
				msg += "; " + n
			}
		}
		if err != nil {
			msg += "; " + err.Error()
		}
		err = errors.New(msg)
	} else {
		res, err = delivery.Uninstall(delivery.UninstallOptions{Paths: paths, Home: home, Backend: be, All: all})
	}
	if app.JSON {
		doc := map[string]any{"schedule": nonNil(res.Schedule), "removed": nonNil(res.Removed), "agents": nonNilResults(agents)}
		if err != nil {
			doc["error"] = fmt.Sprintf("sapulidi: uninstall: %v", err)
		}
		writeJSON(app.Stdout, doc)
		if err != nil {
			fmt.Fprintf(app.Stderr, "sapulidi: uninstall: %v\n", err)
			return 1
		}
		return 0
	}
	for _, l := range res.Schedule {
		fmt.Fprintf(app.Stdout, "removed schedule %s (%s)\n", l, be.Name())
	}
	for _, p := range res.Removed {
		fmt.Fprintf(app.Stdout, "removed %s\n", safeText(p))
	}
	if err != nil {
		return fail(app, "uninstall", err)
	}
	if len(res.Removed) == 0 {
		fmt.Fprintln(app.Stdout, "no binaries found; nothing else to remove")
	}
	return 0
}
