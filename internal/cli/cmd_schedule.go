package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/platform"
	"github.com/als0x/sapulidi/internal/schedule"
	"github.com/als0x/sapulidi/internal/state"
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

const scheduleUsage = "usage: sapulidi schedule install [run] [--every <interval>] | remove|status [run|agent]"

type scheduleStatusJSON struct {
	Backend     string   `json:"backend"`
	Label       string   `json:"label"`
	Installed   bool     `json:"installed"`
	Interval    string   `json:"interval"`
	NextRun     string   `json:"next_run"`
	DryRunsOnly bool     `json:"dry_runs_only"`
	Warnings    []string `json:"warnings"`
}

// takeEvery removes `--every <v>` or `--every=<v>` from args.
func takeEvery(args []string) (rest []string, every string, given, ok bool) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--every":
			if i+1 >= len(args) || given {
				return nil, "", false, false
			}
			every, given = args[i+1], true
			i++
		case strings.HasPrefix(a, "--every="):
			if given {
				return nil, "", false, false
			}
			every, given = strings.TrimPrefix(a, "--every="), true
		default:
			rest = append(rest, a)
		}
	}
	return rest, every, given, true
}

func runSchedule(app *App, args []string) int {
	args, every, hasEvery, ok := takeEvery(args)
	if !ok || len(args) < 1 || len(args) > 2 {
		fmt.Fprintln(app.Stderr, scheduleUsage)
		return 2
	}
	action := args[0]
	target := ""
	if len(args) == 2 {
		target = args[1]
	}
	if (action != "install" && action != "remove" && action != "status") ||
		(target != "" && target != "run" && target != "agent") ||
		(hasEvery && (action != "install" || target == "agent")) {
		fmt.Fprintln(app.Stderr, scheduleUsage)
		return 2
	}
	var everyD time.Duration
	if hasEvery {
		d, err := schedule.ParseInterval(every)
		if err != nil {
			fmt.Fprintf(app.Stderr, "sapulidi: schedule install --every: %v\n", err)
			return 2
		}
		everyD = d
	}
	be, paths, err := scheduleSetup()
	if err != nil {
		fmt.Fprintf(app.Stderr, "sapulidi: schedule: %v\n", err)
		return 1
	}
	if action == "install" && paths.Config != "" {
		// A config that cannot be read would silently install at the default interval: refuse instead.
		cfg, _, err := config.Load(paths.Config)
		if err == nil && len(cfg.Invalid) > 0 {
			err = fmt.Errorf("not valid TOML: %s", strings.Join(cfg.Invalid, ", "))
		}
		if err != nil {
			fmt.Fprintf(app.Stderr, "sapulidi: schedule install: the config cannot be read, nothing installed: %v\n", err)
			return 1
		}
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
	if hasEvery {
		for i := range jobs {
			if jobs[i].Label == schedule.LabelRun {
				jobs[i].Interval = everyD
			}
		}
	}
	switch action {
	case "install":
		// The new interval is stored before anything is installed, so a store that fails installs nothing and a
		// later bare install can never revert what the scheduler runs. A failed install puts the old value back.
		undo := func() {}
		if hasEvery {
			prev, had, err := config.AppliedScheduleInterval(paths)
			if err == nil {
				err = config.Apply(paths, config.Change{Key: "schedule.interval", Value: every})
			}
			if err != nil {
				fmt.Fprintf(app.Stderr, "sapulidi: schedule install: %v\n", err)
				return 1
			}
			undo = func() {
				if err := config.RestoreScheduleInterval(paths, prev, had); err != nil {
					fmt.Fprintf(app.Stderr, "sapulidi: schedule install: could not restore the previous interval: %v\n", err)
				}
			}
		}
		for _, j := range jobs {
			if err := os.MkdirAll(filepath.Dir(j.LogPath), 0o700); err != nil {
				undo()
				fmt.Fprintf(app.Stderr, "sapulidi: schedule: %v\n", err)
				return 1
			}
			if err := be.Install(j); err != nil {
				undo()
				fmt.Fprintf(app.Stderr, "sapulidi: schedule install %s: %v\n", j.Label, err)
				return 1
			}
			if !app.JSON {
				fmt.Fprintf(app.Stdout, "installed %s (%s), every %s\n", j.Label, be.Name(), config.FormatInterval(j.Interval))
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
		return printScheduleStatus(app, be, paths, jobs)
	}
	return 0
}

// consentRecorded reports whether setup recorded a readable consent. Like a run, it fails closed: a missing state
// directory, an unreadable, empty or corrupt consent file all count as no consent. A state directory that does
// not exist yet is not created.
func consentRecorded(paths config.Paths) bool {
	if _, err := os.Stat(paths.State); err != nil {
		return false
	}
	st, err := state.Open(paths)
	if err != nil {
		return false
	}
	c, err := st.Consent()
	return err == nil && c != nil
}

func printScheduleStatus(app *App, be schedule.Backend, paths config.Paths, jobs []schedule.Job) int {
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
		// A job installed before consent only produces dry runs (spec 14.7); only the run job is gated.
		dry := st.Installed && j.Label == schedule.LabelRun && !consentRecorded(paths)
		out = append(out, scheduleStatusJSON{be.Name(), j.Label, st.Installed, config.FormatInterval(j.Interval), st.Next, dry, w})
		state := "not installed"
		if st.Installed {
			state = "installed"
		}
		line := fmt.Sprintf("%s: %s (%s) every %s", j.Label, state, be.Name(), config.FormatInterval(j.Interval))
		if st.Next != "" {
			line += ", next run in " + st.Next
		}
		if st.Detail != "" {
			line += " " + st.Detail
		}
		text = append(text, line)
		if dry {
			text = append(text, "  dry runs only: no consent recorded")
		}
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
