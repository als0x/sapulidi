package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/platform"
	"github.com/als0x/sapulidi/internal/schedule"
)

// scheduleSetup resolves the backend and the paths; tests replace it.
var scheduleSetup = func() (schedule.Backend, config.Paths, error) {
	p, err := config.ResolvePaths()
	if err != nil {
		return nil, config.Paths{}, err
	}
	run := execx.Real{}
	return schedule.Pick(platform.New(run), run), p, nil
}

func init() {
	Register(Command{Name: "schedule", Summary: "install, remove or show the scheduled run", Run: runSchedule})
}

const scheduleUsage = "usage: sapulidi schedule install|remove|status [run|agent]"

type scheduleStatusJSON struct {
	Backend   string   `json:"backend"`
	Label     string   `json:"label"`
	Installed bool     `json:"installed"`
	Warnings  []string `json:"warnings"`
}

func runSchedule(app *App, args []string) int {
	if len(args) < 1 || len(args) > 2 {
		fmt.Fprintln(app.Stderr, scheduleUsage)
		return 2
	}
	action := args[0]
	target := ""
	if len(args) == 2 {
		target = args[1]
	}
	if (action != "install" && action != "remove" && action != "status") ||
		(target != "" && target != "run" && target != "agent") {
		fmt.Fprintln(app.Stderr, scheduleUsage)
		return 2
	}
	be, paths, err := scheduleSetup()
	if err != nil {
		fmt.Fprintf(app.Stderr, "sapulidi: schedule: %v\n", err)
		return 1
	}
	var all, jobs []schedule.Job
	all = schedule.Jobs(paths)
	for _, j := range all {
		short := strings.TrimPrefix(j.Label, "dev.sapulidi.")
		// install defaults to the run job (the agent slot is opt-in); remove and status default to both.
		if target == short || (target == "" && (action != "install" || short == "run")) {
			jobs = append(jobs, j)
		}
	}
	switch action {
	case "install":
		for _, j := range jobs {
			if err := os.MkdirAll(filepath.Dir(j.LogPath), 0o700); err != nil {
				fmt.Fprintf(app.Stderr, "sapulidi: schedule: %v\n", err)
				return 1
			}
			if err := be.Install(j); err != nil {
				fmt.Fprintf(app.Stderr, "sapulidi: schedule install %s: %v\n", j.Label, err)
				return 1
			}
			if !app.JSON {
				fmt.Fprintf(app.Stdout, "installed %s (%s)\n", j.Label, be.Name())
			}
		}
	case "remove":
		for _, j := range jobs {
			if err := be.Remove(j.Label); err != nil {
				fmt.Fprintf(app.Stderr, "sapulidi: schedule remove %s: %v\n", j.Label, err)
				return 1
			}
			if !app.JSON {
				fmt.Fprintf(app.Stdout, "removed %s (%s)\n", j.Label, be.Name())
			}
		}
	}
	if action == "status" || app.JSON {
		return printScheduleStatus(app, be, jobs)
	}
	return 0
}

func printScheduleStatus(app *App, be schedule.Backend, jobs []schedule.Job) int {
	out := make([]scheduleStatusJSON, 0, len(jobs))
	text := []string{}
	for _, j := range jobs {
		st, err := be.Status(j.Label)
		if err != nil {
			fmt.Fprintf(app.Stderr, "sapulidi: schedule status %s: %v\n", j.Label, err)
			return 1
		}
		w := append([]string{}, st.Warnings...)
		if st.Installed {
			w = append(w, schedule.EnvDrift(be, j)...)
		}
		out = append(out, scheduleStatusJSON{be.Name(), j.Label, st.Installed, w})
		state := "not installed"
		if st.Installed {
			state = "installed"
		}
		line := fmt.Sprintf("%s: %s (%s)", j.Label, state, be.Name())
		if st.Detail != "" {
			line += " " + st.Detail
		}
		text = append(text, line)
		for _, x := range w {
			text = append(text, "  warning: "+x)
		}
	}
	if app.JSON {
		b, _ := json.Marshal(out)
		fmt.Fprintln(app.Stdout, string(b))
		return 0
	}
	for _, l := range text {
		fmt.Fprintln(app.Stdout, l)
	}
	return 0
}
