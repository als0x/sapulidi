package cli

import (
	"fmt"

	"github.com/als0x/sapulidi/internal/report/explain"
)

func init() {
	Register(Command{Name: "explain", Summary: "facts about one path: size, owner, git, matching rule, guard", Run: runExplain})
}

func runExplain(app *App, args []string) int {
	fs := newFlags(app, "explain")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		return usageFail(app, "usage: sapulidi explain <path>")
	}
	env, err := app.Build()
	if err != nil {
		return fail(app, "explain", err)
	}
	e, err := explain.Path(env, fs.Arg(0))
	if err != nil {
		return fail(app, "explain", err)
	}
	if app.JSON {
		writeJSON(app.Stdout, e)
		return 0
	}
	out := app.Stdout
	fmt.Fprintf(out, "path:   %s (%s)\n", e.Path, e.Kind)
	size := humanBytes(e.Bytes)
	if !e.SizeKnown {
		size += " (partial)"
	}
	fmt.Fprintf(out, "size:   %s\n", size)
	if e.Truncated {
		fmt.Fprintln(out, "note:   the 30 s limit ended; size and guards are incomplete")
	}
	if e.Owner != "" {
		fmt.Fprintf(out, "owner:  %s\n", e.Owner)
	}
	if e.Newest != "" {
		fmt.Fprintf(out, "newest: %s\n", e.Newest)
	}
	if e.Git != nil {
		fmt.Fprintf(out, "git:    %s in %s\n", e.Git.Status, e.Git.Repo)
	}
	if e.Rule != "" {
		fmt.Fprintf(out, "rule:   %s\n", e.Rule)
	} else {
		fmt.Fprintln(out, "rule:   none matches")
	}
	if e.Keeps != nil {
		fmt.Fprintf(out, "kept:   %s %s\n", e.Keeps.Why, e.Keeps.Detail)
	} else {
		fmt.Fprintln(out, "kept:   no guard keeps it")
	}
	return 0
}
