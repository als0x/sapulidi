package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/als0x/sapulidi/internal/agent"
	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/platform"
	"github.com/als0x/sapulidi/internal/report"
)

func init() {
	Register(Command{Name: "agent", Summary: "the opt-in scheduled agent: 'run [--trigger t] [--force]'", Run: runAgent})
}

const agentUsage = "usage: sapulidi agent run [--trigger <t>] [--force]"

func runAgent(app *App, args []string) int {
	if len(args) == 0 || args[0] != "run" {
		return usageFail(app, "%s", agentUsage)
	}
	fs := newFlags(app, "agent run")
	trigger := fs.String("trigger", "manual", "what started the run")
	force := fs.Bool("force", false, "skip the timing (not the opt-in)")
	if code, ok := parseFlags(app, fs, args[1:]); !ok {
		return code
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return fail(app, "agent run", err)
	}
	cfg, _, err := config.Load(paths.Config)
	if err != nil {
		return fail(app, "agent run", err)
	}
	st, err := OpenStore()
	if err != nil {
		return fail(app, "agent run", err)
	}
	// The agent may run only this exact binary, by its real absolute path.
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return fail(app, "agent run", fmt.Errorf("cannot resolve the sapulidi binary: %w", err))
	}
	run := execx.Real{}
	deps := agent.RunDeps{
		Cfg: cfg, Paths: paths, Store: st, Runner: agent.NewRunner(os.Environ()), Bin: exe, Now: time.Now(),
		Anomaly: func() bool {
			env, err := app.Build()
			if err != nil {
				return false
			}
			r, err := report.Build(env, core.Categories(), st)
			return err == nil && len(r.Anomalies) > 0
		},
	}
	if cfg.Notify.Enabled {
		plat := platform.New(run)
		deps.Notify = func(text string) error { return plat.Notify("sapulidi", text) }
	}
	res, err := agent.Run(deps, agent.RunOpts{Trigger: *trigger, Force: *force})
	if err != nil {
		return fail(app, "agent run", fmt.Errorf("%s", safeText(err.Error())))
	}
	saved := res.Saved
	if saved == nil {
		saved = []string{}
	}
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"ran": res.Ran, "reason": res.Reason, "saved": saved,
			"dropped": res.Dropped, "cost_usd": res.CostUSD})
		return 0
	}
	if !res.Ran {
		fmt.Fprintln(app.Stdout, "agent: "+res.Reason)
		return 0
	}
	fmt.Fprintf(app.Stdout, "agent: %d proposals saved, %d dropped (spent %.4f USD)\n", len(saved), res.Dropped, res.CostUSD)
	return 0
}
