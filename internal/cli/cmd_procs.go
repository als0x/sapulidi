package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/procs"
)

func init() {
	Register(Command{Name: "procs", Summary: "orphaned dev servers and helpers; 'stop <pid>' needs a terminal", Run: runProcs})
}

type procJSON struct {
	PID     int       `json:"pid"`
	Start   time.Time `json:"start"`
	Command string    `json:"command"`
	Cwd     string    `json:"cwd"`
	Ports   []int     `json:"ports"`
	Reason  string    `json:"reason"`
}

func runProcs(app *App, args []string) int {
	if len(args) == 0 {
		return listProcs(app)
	}
	if args[0] != "stop" {
		return usageFail(app, "sapulidi procs: unexpected argument %q (usage: sapulidi procs [stop <pid>])", args[0])
	}
	if len(args) != 2 {
		return usageFail(app, "usage: sapulidi procs stop <pid>")
	}
	pid, err := strconv.Atoi(args[1])
	if err != nil || pid <= 1 {
		return usageFail(app, "sapulidi procs stop: %q is not a process id", args[1])
	}
	return stopProc(app, pid)
}

func listProcs(app *App) int {
	env, err := app.Build()
	if err != nil {
		return fail(app, "procs", err)
	}
	res, err := procs.List(env)
	if err != nil {
		return fail(app, "procs", err)
	}
	list := res.Procs
	if app.JSON {
		rows := make([]procJSON, 0, len(list))
		for _, p := range list {
			ports := p.Ports
			if ports == nil {
				ports = []int{}
			}
			rows = append(rows, procJSON{p.PID, p.Start, p.Command, p.Cwd, ports, p.Reason})
		}
		warnings := res.Warnings
		if warnings == nil {
			warnings = []string{}
		}
		writeJSON(app.Stdout, map[string]any{"processes": rows, "warnings": warnings})
		return 0
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(app.Stdout, "note: "+w)
	}
	if len(list) == 0 {
		fmt.Fprintln(app.Stdout, "no orphaned processes under your roots")
		return 0
	}
	now := time.Now()
	fmt.Fprintf(app.Stdout, "%-7s %-5s %-80s  %s\n", "pid", "age", "command", "cwd, ports, reason")
	for _, p := range list {
		ports := "-"
		for i, port := range p.Ports {
			if i == 0 {
				ports = ""
			} else {
				ports += ","
			}
			ports += strconv.Itoa(port)
		}
		fmt.Fprintf(app.Stdout, "%-7d %-5s %-80s  %s, %s, %s\n", p.PID, age(now.Sub(p.Start)), trim(sanitize(p.Command), 80), sanitize(p.Cwd), ports, sanitize(p.Reason))
	}
	return 0
}

func stopProc(app *App, pid int) int {
	env, err := app.Build()
	if err != nil {
		return fail(app, "procs stop", err)
	}
	exited, err := procs.Stop(env, pid)
	switch {
	case errors.Is(err, confirm.ErrNoTerminal):
		// Spike SP7: no terminal for the agent and hooks, and the `!` form is untested, so it is not promised.
		msg := fmt.Sprintf("run this in your own terminal: sapulidi procs stop %d", pid)
		fmt.Fprintln(app.Stderr, msg)
		if app.JSON {
			writeJSON(app.Stdout, map[string]any{"error": msg})
		}
		return 1
	case errors.Is(err, confirm.ErrMismatch):
		return fail(app, "procs stop", errors.New("not confirmed, nothing stopped"))
	case err != nil:
		return fail(app, "procs stop", err)
	}
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"pid": pid, "signal": "SIGTERM", "exited": exited})
	} else if exited {
		fmt.Fprintf(app.Stdout, "sent SIGTERM to %d; it exited\n", pid)
	} else {
		fmt.Fprintf(app.Stdout, "sent SIGTERM to %d; still running after 10 s (sapulidi never uses SIGKILL)\n", pid)
	}
	if !exited {
		return 1
	}
	return 0
}

// sanitize replaces every control rune (ESC, CR, backspace, NUL, ...) with '?': a process command or
// directory is untrusted text and must not be able to rewrite what the terminal shows.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, s)
}

func trim(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func age(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d >= time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d >= time.Minute:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	}
	return "<1m"
}
