package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/core"
)

// largeTranscripts is the Claude Code transcript total above which the report suggests a lower
// cleanupPeriodDays (spec 3.1).
var largeTranscripts int64 = 2 << 30

// transcripts is agent-transcripts. Claude Code sweeps its own transcripts by cleanupPeriodDays (spike SP5), so
// sapulidi only reports them. Codex sessions have no such sweep: files older than the rule's age are removed.
type transcripts struct{}

func (*transcripts) Name() string        { return "agent-transcripts" }
func (*transcripts) Bucket() core.Bucket { return core.Optional }

// cleanupPeriod returns the cleanupPeriodDays of settings.json as text: "30 (default)" when the file or the key
// is absent, "unreadable" when the file cannot be read or parsed, "invalid" when the value is not a whole number
// of days (an integer, an integer-valued float or a string holding an integer).
func cleanupPeriod(env *core.Env) string {
	data, err := os.ReadFile(filepath.Join(claudeDir(env), "settings.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return "30 (default)"
	}
	if err != nil {
		return "unreadable"
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return "unreadable"
	}
	raw, ok := m["cleanupPeriodDays"]
	if !ok {
		return "30 (default)"
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil && f >= 0 && f == math.Trunc(f) && f < 1e9 {
		return strconv.FormatInt(int64(f), 10)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && n >= 0 {
			return strconv.FormatInt(n, 10)
		}
	}
	return "invalid"
}

// transcriptBytes totals the files under projects, leaving out auto memory (projects/<project>/memory).
func transcriptBytes(env *core.Env, projects string) int64 {
	var total int64
	_ = filepath.WalkDir(projects, func(p string, d fs.DirEntry, err error) error {
		if err != nil || cancelled(env) {
			return nil
		}
		if d.IsDir() && d.Name() == "memory" && filepath.Dir(filepath.Dir(p)) == projects {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", (n+1023)/1024)
}

// sessionAge is how old a Codex session file must be: the rule's age or the configured one, never below 24 h.
func sessionAge(env *core.Env, rule core.Rule) time.Duration {
	age := rule.MinAge
	if env.Settings != nil {
		age = env.Settings.Age("agent-transcripts", age)
	}
	if age < 24*time.Hour {
		age = 24 * time.Hour
	}
	return age
}

func (*transcripts) Scan(env *core.Env) ([]core.Candidate, []core.Kept, error) {
	var cands []core.Candidate
	for _, rule := range rulesAt(env, "agent-transcripts", locClaudeTranscripts) {
		projects := filepath.Join(claudeDir(env), "projects")
		if fi, err := os.Lstat(projects); err != nil || !fi.IsDir() {
			continue
		}
		total := transcriptBytes(env, projects)
		reason := fmt.Sprintf("Claude Code removes its own transcripts after cleanupPeriodDays (now %s); %s in total, "+
			"never removed by sapulidi", cleanupPeriod(env), humanBytes(total))
		if total > largeTranscripts {
			reason += "; consider a lower cleanupPeriodDays in settings.json"
		}
		cands = append(cands, core.Candidate{
			Category: "agent-transcripts", RuleKey: rule.Key(), Path: projects, Base: filepath.Dir(projects),
			Removal: core.ReportOnly, Reason: reason,
		})
	}
	for _, rule := range rulesAt(env, "agent-transcripts", locCodexSessions) {
		sessions := filepath.Join(env.Home, ".codex", "sessions")
		age := sessionAge(env, rule)
		_ = filepath.WalkDir(sessions, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if cancelled(env) {
				return filepath.SkipAll
			}
			if !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".jsonl") {
				return nil
			}
			if fi, err := d.Info(); err != nil || env.Now.Sub(fi.ModTime()) < age {
				return nil
			}
			cands = append(cands, core.Candidate{
				Category: "agent-transcripts", RuleKey: rule.Key(), Path: p, Base: filepath.Dir(p), Removal: core.RenameDelete,
				Reason: "Codex session untouched for " + age.String(),
			})
			return nil
		})
	}
	return cands, nil, nil
}

func (*transcripts) Proof(env *core.Env, c core.Candidate) error {
	rule, ok := ruleByKey(env, "agent-transcripts", c.RuleKey)
	if !ok {
		return core.Keep("error", "no agent-transcripts rule "+c.RuleKey)
	}
	if rule.Location != locCodexSessions || c.Removal != core.RenameDelete {
		return core.Keep("error", "report-only: "+c.Path)
	}
	sessions := filepath.Join(env.Home, ".codex", "sessions")
	if !below(sessions, c.Path) || filepath.Dir(c.Path) != c.Base {
		return core.Keep("place", "not a Codex session file: "+c.Path)
	}
	fi, err := os.Lstat(c.Path)
	if err != nil {
		return core.Keep("error", err.Error())
	}
	if !fi.Mode().IsRegular() || !strings.HasSuffix(c.Path, ".jsonl") {
		return core.Keep("place", "not a regular file: "+c.Path)
	}
	if env.Now.Sub(fi.ModTime()) < sessionAge(env, rule) {
		return core.Keep("young", "session changed recently: "+c.Path)
	}
	return nil
}
