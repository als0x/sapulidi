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
		for _, j := range jobs {
			if err := os.MkdirAll(filepath.Dir(j.LogPath), 0o700); err != nil {
				fmt.Fprintf(app.Stderr, "sapulidi: schedule: %v\n", err)
				return 1
			}
			if err := be.Install(j); err != nil {
				fmt.Fprintf(app.Stderr, "sapulidi: schedule install %s: %v\n", j.Label, err)
				return 1
			}
			// Stored only once the backend accepted the interval, so a refusal keeps the old value.
			if hasEvery && j.Label == schedule.LabelRun {
				if err := config.Apply(paths, config.Change{Key: "schedule.interval", Value: every}); err != nil {
					fmt.Fprintf(app.Stderr, "sapulidi: schedule install: %v\n", err)
					return 1
				}
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

// consentRecorded reports whether setup has recorded consent in the state directory. It only looks for the file:
// an unknown state directory counts as recorded, so no note is printed on a guess.
func consentRecorded(paths config.Paths) bool {
	if paths.State == "" {
		return true
	}
	_, err := os.Stat(filepath.Join(paths.State, "consent.json"))
	return err == nil
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
