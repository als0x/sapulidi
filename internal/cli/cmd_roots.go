package cli

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/platform"
	"github.com/als0x/sapulidi/internal/roots"
)

func init() {
	Register(Command{Name: "roots", Summary: "discovered roots, and the ones dropped with the reason", Run: runRoots})
}

// discoverRoots runs root discovery like the env build does, but keeps the dropped candidates; tests replace it.
var discoverRoots = func() ([]core.Root, []core.Kept, error) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return nil, nil, err
	}
	cfg, _, err := config.Load(paths.Config)
	if err != nil {
		return nil, nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	run := execx.Real{}
	plat := platform.New(run)
	tempDir, _ := plat.TempDir()
	found, kept := roots.Discover(roots.DiscoverEnv{
		Home: home, Config: cfg, Platform: plat, Run: run, Now: time.Now(),
		TempDirs: []string{tempDir, "/tmp", "/private/tmp"},
	})
	return found, kept, nil
}

// safeText replaces every control rune and every Unicode format rune (category Cf: bidi overrides such as U+202E,
// isolates, zero-width marks) with '?'. Text from a transcript or the file system is untrusted and must not be
// able to rewrite or reorder what the terminal shows. (sanitize in cmd_procs.go maps controls only.)
func safeText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return '?'
		}
		return r
	}, s)
}

const budgetNote = "discovery stopped at its time budget; some roots may be missing"

// skipSentences say in words why discovery dropped a folder (the tokens of roots.Discover).
var skipSentences = map[string]string{
	"deny":    "never scanned as a project folder",
	"temp":    "temporary folder",
	"not-git": "not a repository and holds none",
	"owner":   "owned by another user",
	"device":  "on another filesystem",
	"missing": "missing or not a folder",
	"cap":     "over the limit on the number of roots",
	"nested":  "inside another root",
	"outside": "its main repository is outside the folder",
}

// skipSentence is the sentence for a token; a token without one is shown as it is.
func skipSentence(why string) string {
	if s, ok := skipSentences[why]; ok {
		return s
	}
	return why
}

func runRoots(app *App, args []string) int {
	if len(args) != 0 {
		return usageFail(app, "usage: sapulidi roots")
	}
	found, kept, err := discoverRoots()
	if err != nil {
		return fail(app, "roots", err)
	}
	type rootRow struct {
		Path   string `json:"path"`
		Source string `json:"source"`
	}
	type dropRow struct {
		Path   string `json:"path"`
		Reason string `json:"reason"`
	}
	rows, drops, notes := []rootRow{}, []dropRow{}, []string{}
	// A path comes from a transcript's cwd and is not trusted: control and escape characters never reach the output.
	for _, r := range found {
		rows = append(rows, rootRow{safeText(r.Path), safeText(r.Source)})
	}
	for _, k := range kept {
		switch k.Why {
		case "no-codex": // core.Kept has no detail field, so the sentence is in Path
			notes = append(notes, safeText(k.Path))
		case "budget":
			notes = append(notes, budgetNote)
		default:
			drops = append(drops, dropRow{safeText(k.Path), safeText(k.Why)})
		}
	}
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"roots": rows, "dropped": drops, "notes": notes})
		return 0
	}
	for _, n := range notes {
		fmt.Fprintln(app.Stdout, "note: "+n)
	}
	if len(rows) == 0 {
		fmt.Fprintln(app.Stdout, "no roots found")
	}
	for _, r := range rows {
		fmt.Fprintf(app.Stdout, "%-8s %s\n", r.Source, r.Path)
	}
	if len(drops) > 0 {
		fmt.Fprintln(app.Stdout, "dropped:")
		for _, d := range drops {
			fmt.Fprintf(app.Stdout, "  %-8s %s (%s)\n", d.Reason, d.Path, skipSentence(d.Reason))
		}
	}
	return 0
}
