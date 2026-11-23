package cli

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/platform"
	"github.com/als0x/sapulidi/internal/schedule"
	"github.com/als0x/sapulidi/internal/version"
)

func init() {
	Register(Command{Name: "doctor", Summary: "check the environment: git, lsof, scheduler, agent CLI, disk", Run: runDoctor})
}

// doctorEnv supplies the platform and the runner doctor probes; tests replace it.
var doctorEnv = func() (core.Platform, core.Runner) {
	run := execx.Real{}
	return platform.New(run), run
}

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "ok" | "warn"
	Detail string `json:"detail"`
}

func runDoctor(app *App, args []string) int {
	if len(args) != 0 {
		return usageFail(app, "usage: sapulidi doctor")
	}
	plat, run := doctorEnv()
	checks := []check{}
	add := func(name string, ok bool, detail string) {
		status := "ok"
		if !ok {
			status = "warn"
		}
		checks = append(checks, check{name, status, detail})
	}
	has := func(tool string) bool { _, err := run.LookPath(tool); return err == nil }
	yesNo := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}

	add("os", true, plat.OS())
	add("arch", true, runtime.GOARCH)
	add("wsl", true, yesNo(plat.IsWSL()))
	add("version", true, version.Version)

	be, paths, err := scheduleSetup()
	if err != nil {
		add("config", false, "cannot resolve the paths: "+err.Error())
	} else {
		add("config", true, paths.Config)
		add("state", true, paths.State)
		add("data", true, paths.Data)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := run.Run(ctx, "", "git", "--version"); err != nil {
		add("git", false, "git not found or not working; worktree and repository guards need it")
	} else {
		add("git", true, strings.TrimSpace(string(out)))
	}

	if plat.OS() == "darwin" {
		if has("lsof") {
			add("open-files", true, "lsof")
		} else {
			add("open-files", false, "lsof not found; files in use cannot be told apart, so they count as in use")
		}
	} else if _, err := os.Stat("/proc/self/fd"); err == nil {
		add("open-files", true, "/proc")
	} else {
		add("open-files", false, "/proc is not readable; files in use cannot be told apart, so they count as in use")
	}

	notifier := "notify-send"
	if plat.OS() == "darwin" {
		notifier = "osascript"
	}
	if has(notifier) {
		add("notifier", true, notifier)
	} else {
		add("notifier", false, notifier+" not found; no desktop notifications")
	}

	if be == nil {
		add("scheduler", false, "no scheduler backend")
	} else {
		var parts []string
		all := true
		for _, j := range schedule.Jobs(paths) {
			st, err := be.Status(j.Label)
			switch {
			case err != nil:
				all = false
				parts = append(parts, j.Label+": "+err.Error())
			case !st.Installed:
				all = false
				parts = append(parts, j.Label+": not installed")
			default:
				parts = append(parts, j.Label+": installed")
				if drift := schedule.EnvDrift(be, j); len(drift) > 0 {
					all = false
					parts = append(parts, strings.Join(drift, "; "))
				}
			}
		}
		add("scheduler", all, be.Name()+"; "+strings.Join(parts, "; "))
	}

	cfg, _, _ := config.Load(paths.Config)
	agentOK, agentDetail := agentFound(cfg, has)
	add("agent", agentOK, agentDetail)

	home, _ := os.UserHomeDir()
	if free, err := plat.FreeBytes(home); err != nil {
		add("disk", false, "cannot read free disk space: "+err.Error())
	} else {
		gb := free >> 30
		add("disk", free >= uint64(cfg.LowDiskGB)<<30, fmt.Sprintf("%d GB free (low_disk_gb is %d)", gb, cfg.LowDiskGB))
	}

	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"checks": checks})
		return 0
	}
	for _, c := range checks {
		fmt.Fprintf(app.Stdout, "%-4s %-11s %s\n", c.Status, c.Name, safeText(c.Detail))
	}
	return 0
}

// agentFound looks for the configured agent CLI, or for claude and codex when none is configured. Whether the
// CLI is logged in cannot be probed without running it (spike SP1 found no side-effect-free check), so the
// scheduled agent logs the failure when it first runs and the line says so.
func agentFound(cfg *config.Config, has func(string) bool) (bool, string) {
	names := []string{"claude", "codex"}
	if cfg != nil && cfg.Agent.CLI != "" {
		names = []string{cfg.Agent.CLI}
	}
	var found []string
	for _, n := range names {
		if has(n) {
			found = append(found, n)
		}
	}
	if len(found) == 0 {
		return false, strings.Join(names, " or ") + " not found; the scheduled agent cannot run"
	}
	return true, strings.Join(found, ", ") + " found; login is checked when the agent first runs"
}
