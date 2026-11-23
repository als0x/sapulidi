package schedule

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/state/atomic"
)

// Systemd is a user service plus timer in ~/.config/systemd/user.
type Systemd struct {
	Home    string
	User    string
	PathEnv string
	Run     core.Runner
}

func (b *Systemd) Name() string { return "systemd" }

func (b *Systemd) dir() string { return filepath.Join(b.Home, ".config", "systemd", "user") }

func unitQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "%", "%%")
	s = strings.ReplaceAll(s, "$", "$$")
	return `"` + s + `"`
}

// envQuote quotes an Environment= value: % is a specifier there, but $ is not expanded.
func envQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "%", "%%")
	return `"` + s + `"`
}

func (b *Systemd) service(j Job) string {
	args := make([]string, len(j.Argv))
	for i, a := range j.Argv {
		args[i] = unitQuote(a)
	}
	env := b.EnvFragment(EnvVar{"PATH", pathOrDefault(b.PathEnv)})
	for _, v := range j.Env {
		env += b.EnvFragment(v)
	}
	return "[Unit]\nDescription=sapulidi " + j.Label + "\n\n[Service]\nType=oneshot\n" +
		"ExecStart=" + strings.Join(args, " ") + "\n" +
		"Nice=10\nIOSchedulingClass=idle\n" + env +
		"StandardOutput=null\nStandardError=append:" + strings.ReplaceAll(j.LogPath, "%", "%%") + "\n"
}

func (b *Systemd) EnvFragment(v EnvVar) string {
	return "Environment=" + envQuote(v.Name+"="+v.Value) + "\n"
}

func (b *Systemd) InstalledText(label string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(b.dir(), label+".service"))
	return string(data), err == nil
}

func (b *Systemd) timer(j Job) string {
	return "[Unit]\nDescription=sapulidi " + j.Label + " timer\n\n[Timer]\nOnBootSec=5min\n" +
		fmt.Sprintf("OnUnitActiveSec=%ds\n", int64(j.Interval.Seconds())) +
		"Persistent=true\n\n[Install]\nWantedBy=timers.target\n"
}

func (b *Systemd) Install(j Job) error {
	if err := needHome(b.Home); err != nil {
		return err
	}
	if err := checkJob(j, b.PathEnv); err != nil {
		return err
	}
	if err := atomic.WriteFile(filepath.Join(b.dir(), j.Label+".service"), []byte(b.service(j))); err != nil {
		return err
	}
	if err := atomic.WriteFile(filepath.Join(b.dir(), j.Label+".timer"), []byte(b.timer(j))); err != nil {
		return err
	}
	if _, err := b.Run.Run(ctx(), "", "systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if _, err := b.Run.Run(ctx(), "", "systemctl", "--user", "enable", "--now", j.Label+".timer"); err != nil {
		return fmt.Errorf("systemctl enable: %w", err)
	}
	// enable --now does not restart a running timer, so a changed interval would not apply.
	if _, err := b.Run.Run(ctx(), "", "systemctl", "--user", "restart", j.Label+".timer"); err != nil {
		return fmt.Errorf("systemctl restart: %w", err)
	}
	return nil
}

func (b *Systemd) Remove(label string) error {
	if err := needHome(b.Home); err != nil {
		return err
	}
	// Not enabled is fine; the files are removed either way.
	_, _ = b.Run.Run(ctx(), "", "systemctl", "--user", "disable", "--now", label+".timer")
	for _, ext := range []string{".timer", ".service"} {
		if err := os.Remove(filepath.Join(b.dir(), label+ext)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	_, _ = b.Run.Run(ctx(), "", "systemctl", "--user", "daemon-reload")
	return nil
}

// Status reports linger off as a warning; it never enables linger.
func (b *Systemd) Status(label string) (Status, error) {
	_, err := b.Run.Run(ctx(), "", "systemctl", "--user", "is-enabled", label+".timer")
	if err != nil {
		if _, ok := exitError(err); ok {
			return Status{}, nil
		}
		return Status{}, err
	}
	st := Status{Installed: true}
	out, err := b.Run.Run(ctx(), "", "loginctl", "show-user", b.User, "-p", "Linger", "--value")
	if err != nil || strings.TrimSpace(string(out)) != "yes" {
		st.Warnings = append(st.Warnings, "linger is off: the timer stops when you log out; run `loginctl enable-linger "+
			b.User+"` yourself if you want it to run while logged out (sapulidi never enables it)")
	}
	return st, nil
}
