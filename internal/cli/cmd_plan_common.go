package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
)

// newFlags makes a flag set that reports its errors on the app's stderr.
func newFlags(app *App, name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(app.Stderr)
	return fs
}

// parseFlags parses args; ok is false when the command must return code (2 for a usage error, 0 for -h).
func parseFlags(app *App, fs *flag.FlagSet, args []string) (code int, ok bool) {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0, false
		}
		if app.JSON { // the flag package already wrote the text on stderr
			writeJSON(app.Stdout, map[string]any{"error": err.Error()})
		}
		return 2, false
	}
	if fs.NArg() > 0 {
		return usageFail(app, "sapulidi %s: unexpected argument %q", fs.Name(), fs.Arg(0)), false
	}
	return 0, true
}

// usageFail reports a usage error (exit 2); with --json the error is also one JSON document on stdout.
func usageFail(app *App, format string, a ...any) int {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintln(app.Stderr, msg)
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"error": msg})
	}
	return 2
}

// fail reports a failure (exit 1) of command name; with --json the error is also one JSON document on stdout.
func fail(app *App, name string, err error) int {
	msg := fmt.Sprintf("sapulidi: %s: %v", name, err)
	fmt.Fprintln(app.Stderr, msg)
	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"error": msg})
	}
	return 1
}

// invalidConfigMessage is the line shown when a config file cannot be read as TOML.
func invalidConfigMessage(path string) string {
	return fmt.Sprintf("config file %s is not valid TOML: nothing will run until it is fixed", path)
}

// invalidConfigFiles lists the config files that exist but are not valid TOML (config.Config.Invalid).
func invalidConfigFiles() []string {
	paths, err := config.ResolvePaths()
	if err != nil {
		return nil
	}
	cfg, _, _ := config.Load(paths.Config)
	return cfg.Invalid
}

// nopFacts is the fact sink of an env built outside a run; the engine installs its own during a run.
type nopFacts struct{}

func (nopFacts) Fact(string, string) {}

func writeJSON(w io.Writer, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(`{"error":"cannot encode output"}`)
	}
	fmt.Fprintln(w, string(b))
}

// humanBytes writes a size with one decimal and a binary unit; a negative size is "unknown".
func humanBytes(n int64) string {
	if n < 0 {
		return "unknown"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

type candidateJSON struct {
	Category string `json:"category"`
	Rule     string `json:"rule"`
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Reason   string `json:"reason"`
	Created  string `json:"created,omitempty"` // RFC 3339 UTC; absent when unknown
	Updated  string `json:"updated,omitempty"` // RFC 3339 UTC; absent when unknown
	Source   string `json:"source,omitempty"`
	Label    string `json:"label"`
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

type keptJSON struct {
	Category string `json:"category"`
	Rule     string `json:"rule,omitempty"`
	Path     string `json:"path,omitempty"`
	Why      string `json:"why"`
}

func candidatesJSON(cs []core.Candidate) []candidateJSON {
	out := make([]candidateJSON, 0, len(cs))
	for _, c := range cs {
		out = append(out, candidateJSON{c.Category, c.RuleKey, c.Path, c.Bytes, c.Reason, rfc3339(c.Created), rfc3339(c.Updated), c.Source, string(c.Label)})
	}
	return out
}

func keptsJSON(ks []core.Kept) []keptJSON {
	out := make([]keptJSON, 0, len(ks))
	for _, k := range ks {
		out = append(out, keptJSON{k.Category, k.RuleKey, k.Path, k.Why})
	}
	return out
}

// candidateTotal sums the known sizes; unknown (-1) counts as 0.
func candidateTotal(cs []core.Candidate) (n int, bytes int64) {
	for _, c := range cs {
		n++
		if c.Bytes > 0 {
			bytes += c.Bytes
		}
	}
	return n, bytes
}

// keptByWhy counts kept items per reason and returns the reasons sorted.
func keptByWhy(ks []core.Kept) (map[string]int, []string) {
	counts := map[string]int{}
	for _, k := range ks {
		counts[k.Why]++
	}
	whys := make([]string, 0, len(counts))
	for w := range counts {
		whys = append(whys, w)
	}
	sort.Strings(whys)
	return counts, whys
}

// removableLabels are the labels of the rows a plan would remove, in the order people read them.
var removableLabels = []core.Label{core.LabelSafe, core.LabelRecommended, core.LabelYourCall}

// withLabel returns the candidates of one label, in the order given.
func withLabel(cs []core.Candidate, l core.Label) []core.Candidate {
	var out []core.Candidate
	for _, c := range cs {
		if c.Label == l {
			out = append(out, c)
		}
	}
	return out
}

// planFn and categories are the engine's plan and the registered categories; tests replace them.
var (
	planFn     = engine.Plan
	categories = core.Categories
)

// planOf builds the env and runs engine.Plan on it; ok is false after an error was printed.
func planOf(app *App, name string) (*core.Env, engine.Summary, bool) {
	env, err := app.Build()
	if err != nil {
		fail(app, name, err)
		return nil, engine.Summary{}, false
	}
	return env, planFn(env, categories(), engine.Options{}), true
}
