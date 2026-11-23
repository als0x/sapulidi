package schedule

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/state/atomic"
)

// Launchd is a per-user LaunchAgent in ~/Library/LaunchAgents.
type Launchd struct {
	Home    string
	UID     int
	PathEnv string // PATH written into the plist: launchd's own default is minimal
	Run     core.Runner
	Stat    func(string) (os.FileInfo, error) // nil means os.Stat; tests replace it
}

func (b *Launchd) Name() string { return "launchd" }

func (b *Launchd) plist(label string) string {
	return filepath.Join(b.Home, "Library", "LaunchAgents", label+".plist")
}

func (b *Launchd) domain(label string) string { return fmt.Sprintf("gui/%d/%s", b.UID, label) }

func xmlText(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func (b *Launchd) render(j Job) string {
	var s strings.Builder
	s.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	s.WriteString("\t<key>Label</key>\n\t<string>" + xmlText(j.Label) + "</string>\n")
	s.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range j.Argv {
		s.WriteString("\t\t<string>" + xmlText(a) + "</string>\n")
	}
	s.WriteString("\t</array>\n")
	fmt.Fprintf(&s, "\t<key>StartInterval</key>\n\t<integer>%d</integer>\n", int64(j.Interval.Seconds()))
	runAtLoad := "<false/>"
	if j.Label == LabelRun {
		runAtLoad = "<true/>"
	}
	s.WriteString("\t<key>RunAtLoad</key>\n\t" + runAtLoad + "\n")
	s.WriteString("\t<key>LowPriorityIO</key>\n\t<true/>\n")
	s.WriteString("\t<key>Nice</key>\n\t<integer>10</integer>\n")
	s.WriteString("\t<key>StandardErrorPath</key>\n\t<string>" + xmlText(j.LogPath) + "</string>\n")
	s.WriteString("\t<key>StandardOutPath</key>\n\t<string>/dev/null</string>\n")
	s.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
	s.WriteString(b.EnvFragment(EnvVar{"PATH", pathOrDefault(b.PathEnv)}))
	for _, v := range j.Env {
		s.WriteString(b.EnvFragment(v))
	}
	s.WriteString("\t</dict>\n")
	s.WriteString("</dict>\n</plist>\n")
	return s.String()
}

func (b *Launchd) EnvFragment(v EnvVar) string {
	return "\t\t<key>" + xmlText(v.Name) + "</key>\n\t\t<string>" + xmlText(v.Value) + "</string>\n"
}

func (b *Launchd) InstalledText(label string) (string, bool) {
	data, err := os.ReadFile(b.plist(label))
	return string(data), err == nil
}

// Install writes the plist atomically, boots out any loaded copy (not-found is fine) and bootstraps it.
func (b *Launchd) Install(j Job) error {
	if err := needHome(b.Home); err != nil {
		return err
	}
	if err := checkJob(j, b.PathEnv); err != nil {
		return err
	}
	stat := b.Stat
	if stat == nil {
		stat = os.Stat
	}
	if _, err := stat(j.Argv[0]); err != nil {
		return fmt.Errorf("schedule: binary %s: %w", j.Argv[0], err)
	}
	path := b.plist(j.Label)
	if err := atomic.WriteFile(path, []byte(b.render(j))); err != nil {
		return err
	}
	// Not loaded yet is the normal case; any bootout failure is ignored, bootstrap decides.
	_, _ = b.Run.Run(ctx(), "", "launchctl", "bootout", b.domain(j.Label))
	if _, err := b.Run.Run(ctx(), "", "launchctl", "bootstrap", fmt.Sprintf("gui/%d", b.UID), path); err != nil {
		_ = os.Remove(path) // do not leave a plist launchd would load at the next login
		return fmt.Errorf("launchctl bootstrap: %w", err)
	}
	return nil
}

func (b *Launchd) Remove(label string) error {
	if err := needHome(b.Home); err != nil {
		return err
	}
	_, _ = b.Run.Run(ctx(), "", "launchctl", "bootout", b.domain(label))
	if err := os.Remove(b.plist(label)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Status: "not installed" is a non-zero exit of `launchctl print`, never a text match.
func (b *Launchd) Status(label string) (Status, error) {
	out, err := b.Run.Run(ctx(), "", "launchctl", "print", b.domain(label))
	if err != nil {
		if _, ok := exitError(err); ok {
			return Status{}, nil
		}
		return Status{}, err
	}
	var parts []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		for _, k := range []string{"runs = ", "last exit code = ", "run interval = "} {
			if strings.HasPrefix(line, k) {
				parts = append(parts, line)
			}
		}
	}
	return Status{Installed: true, Detail: strings.Join(parts, ", ")}, nil
}
