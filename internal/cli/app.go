// Package cli dispatches sapulidi commands.
package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/als0x/sapulidi/internal/core"
)

// App is what every command receives.
type App struct {
	Stdout, Stderr io.Writer
	JSON           bool
	Build          func() (*core.Env, error)
}

// BuildEnv is assigned by the build file (W01) in init(); nil means not available in this build.
var BuildEnv func() (*core.Env, error)

// Main runs one command line and returns the exit code (0 ok, 1 refused or failed, 2 usage).
func Main(args []string, stdout, stderr io.Writer) int {
	app := &App{Stdout: stdout, Stderr: stderr, Build: BuildEnv}
	if app.Build == nil {
		app.Build = func() (*core.Env, error) { return nil, fmt.Errorf("not available in this build") }
	}
	// --json is global: accepted anywhere before a bare "--", dropped before dispatch.
	rest := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if a == "--json" {
			app.JSON = true
			continue
		}
		rest = append(rest, a)
	}
	args = rest
	if len(args) == 0 {
		return noArgs(app)
	}
	if strings.HasPrefix(args[0], "-") && args[0] != "--" {
		return runFlags(app, args)
	}
	c, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "sapulidi: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
	return c.Run(app, args[1:])
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: sapulidi [--json] <command> [args]")
	fmt.Fprintln(w, "run 'sapulidi help' to list commands")
}

func sortedCommands() []Command {
	out := make([]Command, 0, len(commands))
	for _, c := range commands {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func init() {
	Register(Command{Name: "help", Summary: "list commands", Run: runHelp})
}

func runHelp(app *App, _ []string) int {
	fmt.Fprintln(app.Stdout, "usage: sapulidi [--json] <command> [args]")
	fmt.Fprintln(app.Stdout)
	for _, c := range sortedCommands() {
		fmt.Fprintf(app.Stdout, "  %-12s %s\n", c.Name, c.Summary)
	}
	frontHelp(app)
	return 0
}
