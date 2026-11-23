package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// TUI is assigned by the TUI package (T3) in init(); nil means no interactive screen in this build.
var TUI func(app *App) int

// isTerminal reports whether the app writes to the process stdout and both stdin and stdout are terminals.
var isTerminal = func(app *App) bool {
	if app.Stdout != io.Writer(os.Stdout) {
		return false
	}
	return fdIsTTY(os.Stdin.Fd()) && fdIsTTY(os.Stdout.Fd())
}

// frontHosts are the names `--agents` accepts, besides "all"; the install table is the source.
func frontHosts() []string { return hostNames() }

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
	hosts := frontHosts()
	// valueAt returns the value of the flag at i: its "=value" part, or the next argument when that is not a flag.
	valueAt := func(i int) (v string, ok bool, used int) {
		if _, after, found := strings.Cut(args[i], "="); found {
			return after, true, 0
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			return args[i+1], true, 1
		}
		return "", false, 0
	}
	lastAgents := -1 // index into steps of an --agents install that --remove may still flip
	for i := 0; i < len(args); i++ {
		flag, _, _ := strings.Cut(args[i], "=")
		switch flag {
		case "--help", "-h":
			help = true
		case "--agents":
			lastAgents = -1
			name, ok, used := valueAt(i)
			if !ok {
				steps = append(steps, frontStep{command: "agents"})
				continue
			}
			i += used
			if name == "auto" {
				return nil, false, fmt.Errorf("sapulidi: --agents auto is not accepted; name a host (%s) or all", strings.Join(hosts, ", "))
			}
			if name != "all" && !contains(hosts, name) {
				return nil, false, fmt.Errorf("sapulidi: --agents: unknown name %q (valid: %s, all)", name, strings.Join(hosts, ", "))
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
			v, ok, used := valueAt(i)
			i += used
			switch {
			case !ok:
				steps = append(steps, frontStep{command: "schedule", args: []string{"status"}})
			case v == "off":
				steps = append(steps, frontStep{command: "schedule", args: []string{"remove"}})
			default:
				steps = append(steps, frontStep{command: "schedule", args: []string{"install", "--every", v}})
			}
		default:
			return nil, false, fmt.Errorf("sapulidi: unknown flag or command %q", args[i])
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
	if app.JSON && len(steps) > 1 {
		fmt.Fprintln(app.Stderr, "sapulidi: --json prints one document per call; give one flag at a time")
		return 2
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
	fmt.Fprintf(app.Stdout, "  --agents <name>|all     install sapulidi into an agent tool (%s)\n", strings.Join(frontHosts(), ", "))
	fmt.Fprintln(app.Stdout, "  --agents <name>|all --remove   remove it again")
	fmt.Fprintln(app.Stdout, "  --schedule <interval>   install the scheduled run, for example 30m")
	fmt.Fprintln(app.Stdout, "  --schedule off          remove the scheduled run")
	fmt.Fprintln(app.Stdout, "  --schedule              show the schedule status")
}
