// Package schedule installs, removes and reports the OS scheduler entries that run sapulidi
// (launchd on macOS, a systemd user timer or cron on Linux), without sudo.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
)

const (
	LabelRun   = "dev.sapulidi.run"
	LabelAgent = "dev.sapulidi.agent"

	agentInterval = 7 * 24 * time.Hour
)

// ParseInterval reads a schedule interval: <n>[s|m|h|d], a whole number of minutes from 5m to 7d. It does not
// depend on the backend; the cron backend refuses what it cannot express when the job is installed.
func ParseInterval(s string) (time.Duration, error) { return config.ParseScheduleInterval(s) }

// Job is one scheduled command.
type Job struct {
	Label    string
	Argv     []string // argv[0] absolute
	Interval time.Duration
	LogPath  string // where stderr goes
	Env      []EnvVar
}

// EnvVar is one environment variable written into the job next to PATH.
type EnvVar struct{ Name, Value string }

// EnvDrift returns one warning per SAPULIDI_ variable that the installed job of j lacks or holds with another
// value than j. It is empty when the job is not installed or the backend cannot be read.
func EnvDrift(b Backend, j Job) []string {
	d, ok := b.(interface {
		InstalledText(label string) (string, bool)
		EnvFragment(v EnvVar) string
	})
	if !ok {
		return nil
	}
	text, found := d.InstalledText(j.Label)
	if !found {
		return nil
	}
	var out []string
	for _, v := range j.Env {
		if !strings.Contains(text, d.EnvFragment(v)) {
			out = append(out, fmt.Sprintf("%s: the installed job does not carry %s=%s; run `sapulidi schedule install`",
				j.Label, v.Name, v.Value))
		}
	}
	return out
}

// Status is what a backend knows about one label.
type Status struct {
	Installed bool
	Detail    string // next/last run, when the backend says
	Next      string // time until the next run (e.g. "12m34s"), empty when the backend cannot tell
	Warnings  []string
}

// Backend is one OS scheduler.
type Backend interface {
	Name() string
	Install(j Job) error
	Remove(label string) error
	Status(label string) (Status, error)
}

// Jobs returns the run job (every schedule.interval) and the weekly agent job (unchanged by schedule.interval). The binary is always <data>/bin/current/sapulidi,
// never the plugin cache path (which changes on plugin updates).
func Jobs(p config.Paths) []Job {
	bin := filepath.Join(p.Data, "bin", "current", "sapulidi")
	// A scheduler does not read the shell rc: the paths this process resolved from SAPULIDI_*/XDG_* are baked in,
	// or the job would resolve the default config and state and miss the consent and the heartbeat.
	env := func() []EnvVar {
		return []EnvVar{{"SAPULIDI_CONFIG", p.Config}, {"SAPULIDI_STATE", p.State}, {"SAPULIDI_DATA", p.Data}}
	}
	// SAPULIDI_SCHEDULED tells a run that the installed scheduler started it, which the automatic update requires.
	// The agent job does not carry it: the AI agent it starts would inherit it.
	return []Job{
		{Label: LabelRun, Argv: []string{bin, "run", "--trigger", "schedule"}, Interval: config.ScheduleInterval(p),
			LogPath: filepath.Join(p.State, "schedule-run.log"),
			Env:     append(env(), EnvVar{"SAPULIDI_SCHEDULED", "1"})},
		{Label: LabelAgent, Argv: []string{bin, "agent", "run", "--trigger", "schedule"}, Interval: agentInterval,
			LogPath: filepath.Join(p.State, "schedule-agent.log"), Env: env()},
	}
}

// Pick chooses the backend: launchd on darwin; on Linux a systemd user timer when the user manager
// answers `systemctl --user show-environment`, else cron. Files go under the home directory
// ($HOME), so tests that set HOME write under their temp tree.
func Pick(p core.Platform, run core.Runner) Backend {
	home, _ := os.UserHomeDir()
	pathEnv := os.Getenv("PATH")
	username := currentUser()
	if p.OS() == "darwin" {
		return &Launchd{Home: home, UID: os.Getuid(), PathEnv: pathEnv, Run: run}
	}
	reason := ""
	if _, err := run.LookPath("systemctl"); err != nil {
		reason = "systemctl not found"
	} else if _, err := run.Run(context.Background(), "", "systemctl", "--user", "show-environment"); err != nil {
		reason = "systemd user manager not running"
	} else {
		return &Systemd{Home: home, User: username, PathEnv: pathEnv, Run: run}
	}
	return &Cron{Home: home, PathEnv: pathEnv, Run: run,
		Reason: reason + "; using cron. A systemd timer would need `loginctl enable-linger " + username +
			"` to run while logged out (sapulidi never enables it)"}
}

func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if v := os.Getenv("USER"); v != "" {
		return v
	}
	return "$USER"
}

func needHome(home string) error {
	if home == "" || !filepath.IsAbs(home) {
		return fmt.Errorf("schedule: home directory %q is not absolute", home)
	}
	return nil
}

const defaultPath = "/usr/bin:/bin"

// pathOrDefault is the PATH written into a job; an empty one falls back to a minimal safe value.
func pathOrDefault(p string) string {
	if p == "" {
		return defaultPath
	}
	return p
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// checkJob refuses a job whose values could break out of the file format it is written into:
// any control character (newline, CR, NUL, ...) in the label, argv, PATH or log path.
func checkJob(j Job, pathEnv string) error {
	if j.Label == "" || len(j.Argv) == 0 || !filepath.IsAbs(j.Argv[0]) || j.Interval <= 0 {
		return fmt.Errorf("schedule: invalid job %q", j.Label)
	}
	vals := append([]string{j.Label, j.LogPath, pathEnv}, j.Argv...)
	for _, v := range j.Env {
		vals = append(vals, v.Name, v.Value)
	}
	for _, v := range vals {
		if hasControl(v) {
			return fmt.Errorf("schedule: job %q has a control character in %q", j.Label, v)
		}
	}
	return nil
}

// exitError returns the *execx.ExitError inside err, when the command ran and exited non-zero.
func exitError(err error) (*execx.ExitError, bool) {
	var ee *execx.ExitError
	if errors.As(err, &ee) {
		return ee, true
	}
	return nil, false
}

func ctx() context.Context { return context.Background() }

// quoteSh quotes one word for /bin/sh.
func quoteSh(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
