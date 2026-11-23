package cli

import (
	"fmt"
	"os"
	"strings"
)

// TUI is assigned by the TUI package (T3) in init(); nil means no interactive screen in this build.
var TUI func(app *App) int

// isTerminal reports whether stdin and stdout are both terminals.
var isTerminal = func(app *App) bool {
	f, ok := app.Stdout.(*os.File)
	return ok && charDevice(os.Stdin) && charDevice(f)
}

func charDevice(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// frontHosts are the names `--agents` accepts, besides "all".
var frontHosts = []string{"claude", "codex", "cursor", "antigravity", "opencode", "pi"}

type frontStep struct {
	command string
	args    []string
}

const frontHint = "run 'sapulidi --help' for usage"

// noArgs runs the TUI on a terminal, otherwise prints the help.
func noArgs(app *App) int {
	if !app.JSON && TUI != nil && isTerminal(app) {
		return TUI(app)
	}
	return runHelp(app, nil)
}

// parseFront turns leading flags into steps; nothing runs unless the whole line is valid.
func parseFront(args []string) (steps []frontStep, help bool, err error) {
	valueAt := func(i int) (string, bool) {
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			return args[i+1], true
		}
		return "", false
	}
	lastAgents := -1 // index into steps of an --agents install that --remove may still flip
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--help", "-h":
			help = true
		case "--agents":
			lastAgents = -1
			name, ok := valueAt(i)
			if !ok {
				steps = append(steps, frontStep{command: "agents"})
				continue
			}
			i++
			if name == "auto" {
				return nil, false, fmt.Errorf("sapulidi: --agents auto is not accepted; name a host (%s) or all", strings.Join(frontHosts, ", "))
			}
			if name != "all" && !contains(frontHosts, name) {
				return nil, false, fmt.Errorf("sapulidi: --agents: unknown name %q (valid: %s, all)", name, strings.Join(frontHosts, ", "))
			}
			steps = append(steps, frontStep{command: "agents", args: []string{"install", name}})
			lastAgents = len(steps) - 1
		case "--remove":
			if lastAgents < 0 {
				return nil, false, fmt.Errorf("sapulidi: --remove needs --agents <name>|all before it")
			}
			steps[lastAgents].args[0] = "remove"
			lastAgents = -1
		case "--schedule":
			lastAgents = -1
			v, ok := valueAt(i)
			switch {
			case !ok:
				steps = append(steps, frontStep{command: "schedule", args: []string{"status"}})
			case v == "off":
				i++
				steps = append(steps, frontStep{command: "schedule", args: []string{"remove"}})
			default:
				i++
				steps = append(steps, frontStep{command: "schedule", args: []string{"install", "--every", v}})
			}
		default:
			return nil, false, fmt.Errorf("sapulidi: unknown flag or command %q", a)
		}
	}
	return steps, help, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// runFlags runs the flags given; the first failing step decides the exit code.
func runFlags(app *App, args []string) int {
	steps, help, err := parseFront(args)
	if err != nil {
		fmt.Fprintln(app.Stderr, err)
		fmt.Fprintln(app.Stderr, frontHint)
		return 2
	}
	if help {
		return runHelp(app, nil)
	}
	for _, s := range steps {
		c, ok := commands[s.command]
		if !ok {
			fmt.Fprintf(app.Stderr, "sapulidi: unknown command %q\n", s.command)
			return 2
		}
		if code := c.Run(app, s.args); code != 0 {
			return code
		}
	}
	return 0
}

func frontHelp(app *App) {
	fmt.Fprintln(app.Stdout)
	fmt.Fprintln(app.Stdout, "flags:")
	fmt.Fprintln(app.Stdout, "  --help, -h              show this help")
	fmt.Fprintln(app.Stdout, "  --json                  print one JSON document")
	fmt.Fprintln(app.Stdout, "  --agents                list agent tools")
	fmt.Fprintf(app.Stdout, "  --agents <name>|all     install sapulidi into an agent tool (%s)\n", strings.Join(frontHosts, ", "))
	fmt.Fprintln(app.Stdout, "  --agents <name>|all --remove   remove it again")
	fmt.Fprintln(app.Stdout, "  --schedule <interval>   install the scheduled run, for example 30m")
	fmt.Fprintln(app.Stdout, "  --schedule off          remove the scheduled run")
	fmt.Fprintln(app.Stdout, "  --schedule              show the schedule status")
}
