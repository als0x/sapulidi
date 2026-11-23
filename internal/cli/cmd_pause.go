package cli

import "fmt"

func init() {
	Register(Command{Name: "pause", Summary: "stop every run until 'resume'", Run: func(app *App, args []string) int {
		return setPaused(app, "pause", args, true)
	}})
	Register(Command{Name: "resume", Summary: "undo 'pause'", Run: func(app *App, args []string) int {
		return setPaused(app, "resume", args, false)
	}})
}

func setPaused(app *App, name string, args []string, pause bool) int {
	if len(args) != 0 {
		return usageFail(app, "usage: sapulidi %s", name)
	}
	st, err := OpenStore()
	if err != nil {
		return fail(app, name, err)
	}
	if pause {
		st.Pause()
	} else {
		st.Resume()
	}
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"paused": st.Paused()})
	} else if pause {
		fmt.Fprintln(app.Stdout, "paused: no run removes anything until 'sapulidi resume'")
	} else {
		fmt.Fprintln(app.Stdout, "resumed")
	}
	if st.Paused() != pause {
		return fail(app, name, fmt.Errorf("could not change the pause marker"))
	}
	return 0
}
