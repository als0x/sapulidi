package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/exclude"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/install"
	"github.com/als0x/sapulidi/internal/platform"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/internal/update"
	"github.com/als0x/sapulidi/internal/version"
)

// freeBytes answers the free space for home; tests replace it.
var freeBytes = func(home string) (uint64, error) {
	return platform.New(execx.Real{}).FreeBytes(home)
}

func init() {
	Register(Command{Name: "check", Summary: "one-line status for a session start (no scan)", Run: runCheck})
}

// runCheck is the session-start hook: it reads the config and the state files only, never scans or waits for the
// network (a stale update cache is refreshed by a detached child), and prints nothing when there is nothing to say.
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
	// Like every command that decides about deletions, check fails closed on a corrupt exclude.toml.
	if _, err := exclude.Load(paths); err != nil {
		return fail(app, "check", err)
	}
	st, err := state.Open(paths)
	if err != nil {
		return fail(app, "check", err)
	}
	if *host != "" {
		st.Heartbeat(*host, *hostVersion)
	}
	cfg, _, cfgErr := config.Load(paths.Config)

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
		rules, _ := consentRules(paths)
		if n := len(waitingRuleKeys(rules, consent)); n > 0 {
			lines = append(lines, fmt.Sprintf("sapulidi: %d new rule(s) waiting for your OK: run /sapulidi consent", n))
		}
		if cfgErr == nil && len(cfg.Invalid) == 0 {
			if l := upgradeLine(paths.State, rules, consent, cfg, st.Logger()); l != "" {
				lines = append(lines, l)
			}
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
	if cfgErr == nil && updateCheckEnabled(cfg) {
		refreshUpdateCache(paths.State, st.Logger())
		marketplace := install.Marketplace
		if *host != "" && *host != "claude-code" {
			marketplace = "" // only Claude Code has the /plugin command
		}
		if n := updateNotice(paths.State, marketplace); n != "" {
			lines = append(lines, n)
		}
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

// upgradeMarker is the state file that says the 0.3 consent line has been shown.
const upgradeMarker = "upgrade-0.3-consent"

// upgradeLine tells a user who switched optional categories on before 0.3 that those now need consent. It returns
// the line once, then "". The marker is written before the line is shown; one that cannot be written is logged and
// means the line is shown again.
func upgradeLine(stateDir string, rules []core.Rule, consent *config.Consent, cfg *config.Config, log core.Logger) string {
	marker := filepath.Join(stateDir, upgradeMarker)
	if _, err := os.Lstat(marker); err == nil {
		return ""
	}
	var names []string
	recommendedWaits := false
	for _, r := range rules {
		if r.Bucket != core.Optional || slices.Contains(consent.Rules, r.Key()) {
			continue
		}
		recommendedWaits = recommendedWaits || r.Recommend
		if en := cfg.Categories[r.Category].Enabled; en != nil && *en {
			names = append(names, r.Category)
		}
	}
	if len(names) == 0 {
		return ""
	}
	slices.Sort(names)
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		log.Event("upgrade-marker-not-written", map[string]any{"error": err.Error()})
	}
	line := "Sapu Lidi now asks before removing " + strings.Join(slices.Compact(names), ", ") + ": run /sapulidi consent"
	if recommendedWaits {
		line += " (or /sapulidi consent --yes --recommended for the recommended rules)"
	}
	return line
}

// updateCheckEnabled: a development build never checks, and a config file that cannot be read may be the one that
// switches the check off.
func updateCheckEnabled(cfg *config.Config) bool {
	return version.Repo != "" && len(cfg.Invalid) == 0 && cfg.Update.Check
}

// startWait bounds how long check waits for the child to be started (not for it to finish).
const startWait = 100 * time.Millisecond

// updateCheckCommand is `sapulidi update --check-only --quiet` detached: its own session, and no stdin, stdout
// or stderr (nil means the null device).
func updateCheckCommand(exe string) *exec.Cmd {
	cmd := exec.Command(exe, "update", "--check-only", "--quiet")
	cmd.Dir = "/" // the session's directory must not look in use because of this child
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// startUpdateCheck starts that child and does not wait for it. Tests replace it.
var startUpdateCheck = func() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := updateCheckCommand(exe)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// refreshUpdateCache starts a check when the cache is a day old and this process wins the day's claim, so one
// child starts a day however many checks run at once. Only the child writes the cache. A child that cannot be
// started is logged and otherwise ignored.
func refreshUpdateCache(stateDir string, log core.Logger) {
	c, _ := update.LoadCache(stateDir) // an unreadable cache is an empty one
	if update.Recent(c.CheckedAt) {
		return
	}
	notStarted := func(err error) {
		log.Event("update-check-not-started", map[string]any{"error": err.Error()})
	}
	won, err := update.Claim(stateDir)
	if err != nil {
		notStarted(err)
	}
	if !won {
		return
	}
	start := startUpdateCheck
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := start(); err != nil {
			notStarted(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(startWait):
	}
}

// updateNotice is the line about a newer release from the cache, or "". It makes no request.
func updateNotice(stateDir, marketplace string) string {
	c, err := update.LoadCache(stateDir)
	if err != nil {
		return ""
	}
	return update.Notice(version.Version, c, update.Routes(stateDir), marketplace)
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
