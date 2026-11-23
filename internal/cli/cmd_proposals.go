package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/als0x/sapulidi/internal/agent"
	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/core"
)

func init() {
	Register(Command{Name: "proposals", Summary: "the agent's proposals: list; 'show <id>'; 'reject <id>'; 'apply <id> [--yes]'", Run: runProposals})
}

const proposalsUsage = "usage: sapulidi proposals [show <id> | reject <id> | apply <id> [--yes]]"

func runProposals(app *App, args []string) int {
	st, err := OpenStore()
	if err != nil {
		return fail(app, "proposals", err)
	}
	dir := st.ProposalsDir()
	if len(args) == 0 {
		return listProposals(app, dir)
	}
	switch args[0] {
	case "show", "reject":
		if len(args) != 2 {
			return usageFail(app, "%s", proposalsUsage)
		}
		if args[0] == "reject" {
			if err := agent.Remove(dir, args[1]); err != nil {
				return fail(app, "proposals reject", err)
			}
			if app.JSON {
				writeJSON(app.Stdout, map[string]any{"rejected": args[1]})
			} else {
				fmt.Fprintf(app.Stdout, "rejected %s\n", args[1])
			}
			return 0
		}
		p, err := agent.Load(dir, args[1])
		if err != nil {
			return fail(app, "proposals show", err)
		}
		if app.JSON {
			writeJSON(app.Stdout, p)
			return 0
		}
		showProposal(app, p)
		return 0
	case "apply":
		return applyProposal(app, dir, args[1:])
	}
	return usageFail(app, "%s", proposalsUsage)
}

func listProposals(app *App, dir string) int {
	list, err := agent.List(dir)
	if err != nil {
		return fail(app, "proposals", err)
	}
	if app.JSON {
		if list == nil {
			list = []agent.Proposal{}
		}
		writeJSON(app.Stdout, map[string]any{"proposals": list})
		return 0
	}
	if len(list) == 0 {
		fmt.Fprintln(app.Stdout, "no proposals")
		return 0
	}
	for _, p := range list {
		fmt.Fprintf(app.Stdout, "%s  %-16s %-14s %10s  %s\n", safeText(p.ID), safeText(p.Kind), safeText(p.Target),
			humanBytes(p.EstBytes), clipText(safeText(p.Reason), 80))
	}
	return 0
}

func clipText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// showProposal prints every field as text; nothing in a proposal is ever run.
func showProposal(app *App, p agent.Proposal) {
	out := app.Stdout
	fmt.Fprintf(out, "id:        %s\nkind:      %s\ntarget:    %s\n", safeText(p.ID), safeText(p.Kind), safeText(p.Target))
	if p.Value != "" {
		fmt.Fprintln(out, "value:")
		for _, l := range strings.Split(p.Value, "\n") {
			fmt.Fprintln(out, "  "+safeText(l))
		}
	}
	if len(p.Paths) > 0 {
		fmt.Fprintln(out, "paths:")
		for _, x := range p.Paths {
			fmt.Fprintln(out, "  "+safeText(x))
		}
	}
	fmt.Fprintf(out, "estimated: %s\nreason:    %s\n", humanBytes(p.EstBytes), safeText(p.Reason))
	if len(p.Evidence) > 0 {
		fmt.Fprintln(out, "evidence:")
		for _, e := range p.Evidence {
			fmt.Fprintln(out, "  "+safeText(e))
		}
	}
}

func applyProposal(app *App, dir string, args []string) int {
	var id string
	yes := false
	for _, a := range args {
		switch {
		case a == "--yes":
			yes = true
		case id == "" && !strings.HasPrefix(a, "-"):
			id = a
		default:
			return usageFail(app, "%s", proposalsUsage)
		}
	}
	if id == "" {
		return usageFail(app, "%s", proposalsUsage)
	}
	p, err := agent.Load(dir, id)
	if err != nil {
		return fail(app, "proposals apply", err)
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return fail(app, "proposals apply", err)
	}
	d := agent.ApplyDeps{Paths: paths, Yes: yes}
	if p.Kind == agent.KindCleanOnce {
		env, err := app.Build()
		if err != nil {
			return fail(app, "proposals apply", err)
		}
		st, err := OpenStore()
		if err != nil {
			return fail(app, "proposals apply", err)
		}
		d.Env, d.Cats, d.Store = env, core.Categories(), st
	}
	res, err := agent.Apply(d, p)
	if err != nil {
		if errors.Is(err, confirm.ErrNoTerminal) {
			err = fmt.Errorf("%w; this proposal needs an interactive terminal: run it in a terminal yourself", err)
		}
		return fail(app, "proposals apply", fmt.Errorf("%s", safeText(err.Error())))
	}
	_ = agent.Remove(dir, id) // applied: the proposal is done
	if app.JSON {
		removed, notCand := res.Removed, res.NotCandidate
		if removed == nil {
			removed = []string{}
		}
		if notCand == nil {
			notCand = []string{}
		}
		kept := make([]map[string]string, 0, len(res.Kept))
		for _, k := range res.Kept {
			kept = append(kept, map[string]string{"path": k.Path, "why": k.Why})
		}
		writeJSON(app.Stdout, map[string]any{"applied": id, "message": res.Message, "removed": removed,
			"freed_bytes": res.FreedBytes, "kept": kept, "not_a_candidate": notCand})
		return 0
	}
	fmt.Fprintln(app.Stdout, safeText(res.Message))
	for _, k := range res.Kept {
		fmt.Fprintf(app.Stdout, "kept (%s): %s\n", safeText(k.Why), safeText(k.Path))
	}
	for _, x := range res.NotCandidate {
		fmt.Fprintf(app.Stdout, "not a candidate, left alone: %s\n", safeText(x))
	}
	return 0
}
