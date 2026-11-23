package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/platform"
	"github.com/als0x/sapulidi/internal/state"
)

// freeBytes answers the free space for home; tests replace it.
var freeBytes = func(home string) (uint64, error) {
	return platform.New(execx.Real{}).FreeBytes(home)
}

func init() {
	Register(Command{Name: "check", Summary: "one-line status for a session start (no scan)", Run: runCheck})
}

// runCheck is the session-start hook: it reads the config and the state files only, never scans, and prints
// nothing when there is nothing to say.
func runCheck(app *App, args []string) int {
	fs := newFlags(app, "check")
	host := fs.String("host", "", "host that runs this hook, for its heartbeat")
	hostVersion := fs.String("host-version", "", "version the host pins")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return fail(app, "check", err)
	}
	st, err := state.Open(paths)
	if err != nil {
		return fail(app, "check", err)
	}
	if *host != "" {
		st.Heartbeat(*host, *hostVersion)
	}
	cfg, _, _ := config.Load(paths.Config)

	lines := []string{}
	for _, f := range cfg.Invalid {
		lines = append(lines, "sapulidi: "+invalidConfigMessage(f))
	}
	if home, err := os.UserHomeDir(); err == nil {
		limit := uint64(cfg.LowDiskGB) << 30
		if free, err := freeBytes(home); err == nil && free < limit {
			lines = append(lines, fmt.Sprintf("sapulidi: free disk space is %s, below the %d GB limit",
				humanBytes(int64(free)), cfg.LowDiskGB))
		}
	}
	consent, cerr := st.Consent()
	if consent == nil || cerr != nil {
		lines = append(lines, "sapulidi is installed but not set up: run /sapulidi setup")
	} else {
		rules, _ := loadRules(paths)
		if n := len(waitingRuleKeys(rules, consent)); n > 0 {
			lines = append(lines, fmt.Sprintf("sapulidi: %d new rule(s) waiting for your OK: run /sapulidi consent", n))
		}
	}
	if n := countProposals(st.ProposalsDir()); n > 0 {
		lines = append(lines, fmt.Sprintf("sapulidi: %d proposal(s) waiting for your review", n))
	}
	// Only a hook run (--host) consumes the undelivered facts; a manual or --json check just looks.
	var facts []string
	if *host != "" {
		facts = st.PendingFacts()
	} else {
		facts = peekFacts(paths.State)
	}
	for _, f := range facts {
		lines = append(lines, "sapulidi: "+f)
	}

	if app.JSON {
		writeJSON(app.Stdout, map[string]any{"lines": lines})
		return 0
	}
	for _, l := range lines {
		fmt.Fprintln(app.Stdout, l)
	}
	return 0
}

// peekFacts reads the texts of the undelivered facts in <state>/facts.json, ordered by key, and changes nothing.
func peekFacts(stateDir string) []string {
	b, err := os.ReadFile(filepath.Join(stateDir, "facts.json"))
	if err != nil {
		return nil
	}
	var m map[string]struct {
		Text        string `json:"text"`
		Undelivered bool   `json:"undelivered"`
	}
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k, f := range m {
		if f.Undelivered {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k].Text)
	}
	return out
}

func countProposals(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), ".") {
			n++
		}
	}
	return n
}
