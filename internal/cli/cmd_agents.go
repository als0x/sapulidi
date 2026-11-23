package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/als0x/sapulidi"
	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/install"
)

func init() {
	Register(Command{Name: "agents", Summary: "list agent tools; 'agents install|remove <name>|all' puts sapulidi in or out of them", Run: runAgents})
}

const agentsUsage = "usage: sapulidi agents [install|remove <name>|all]"

func runAgents(app *App, args []string) int {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return fail(app, "agents", fmt.Errorf("cannot find the home folder"))
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return fail(app, "agents", err)
	}
	env := &install.Env{Home: home, Data: paths.Data, Plugin: sapulidi.Plugin}
	if !app.JSON {
		env.Log = func(s string) { fmt.Fprintln(app.Stdout, s) }
	}
	switch {
	case len(args) == 0:
		return agentsList(app, env)
	case len(args) == 2 && (args[0] == "install" || args[0] == "remove"):
		name := args[1]
		if name != "all" {
			if _, ok := install.FindHost(name); !ok {
				return usageFail(app, "sapulidi agents: unknown host %q (known: %s, or all)", name, strings.Join(hostNames(), ", "))
			}
		}
		var res []install.Result
		switch {
		case args[0] == "install" && name == "all":
			res = env.InstallAll()
		case args[0] == "install":
			res, err = env.Install(name)
		default:
			res, err = env.Remove(name)
		}
		if err != nil {
			return fail(app, "agents "+args[0], err)
		}
		return agentsReport(app, res)
	}
	return usageFail(app, "%s", agentsUsage)
}

func hostNames() []string {
	var n []string
	for _, h := range install.Hosts {
		n = append(n, h.Name)
	}
	return n
}

func agentsList(app *App, env *install.Env) int {
	rows, lerr := env.List()
	code := 0
	if lerr != nil {
		code = 1
	}
	if app.JSON {
		doc := map[string]any{"hosts": rows}
		if lerr != nil {
			doc["error"] = lerr.Error()
		}
		writeJSON(app.Stdout, doc)
		return code
	}
	if lerr != nil {
		fmt.Fprintf(app.Stderr, "sapulidi: agents: cannot read agents.json: %v\n", lerr)
	}
	for _, r := range rows {
		line := fmt.Sprintf("%-12s %-12s %s", r.Host.Name, map[bool]string{true: "detected", false: "not found"}[r.Present],
			map[bool]string{true: "installed", false: "not installed"}[r.Installed])
		if !r.Host.Tested {
			line += "  untested"
		}
		if r.Detail != "" {
			line += "  (" + r.Detail + ")"
		}
		fmt.Fprintln(app.Stdout, line)
	}
	return code
}

func agentsReport(app *App, res []install.Result) int {
	code := 0
	for _, r := range res {
		if r.Failed() {
			code = 1
		}
	}
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"results": res})
		return code
	}
	for _, r := range res {
		if r.Detail != "" {
			fmt.Fprintf(app.Stdout, "  %s: %s\n", r.Host, r.Detail)
		}
		for _, n := range r.Notes {
			fmt.Fprintf(app.Stdout, "  %s: %s\n", r.Host, n)
		}
	}
	return code
}
