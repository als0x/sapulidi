// Package install puts the embedded sapulidi plugin into agent tools and takes it out again.
package install

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Host is one agent tool. Data and Skills are relative to the user's home.
type Host struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Data    string `json:"-"`
	Skills  string `json:"-"`
	Tested  bool   `json:"tested"`
}

// Hosts is the table of known tools. Only Claude Code is verified. The folders of claude, codex and cursor
// are documented by those tools; the others are the builder's best knowledge and are why those rows are
// "untested": antigravity keeps its data under ~/.gemini/antigravity, opencode under ~/.config/opencode, and
// pi under ~/.pi/agent.
var Hosts = []Host{
	{Name: "claude", Command: "claude", Data: ".claude", Skills: ".claude/skills", Tested: true},
	{Name: "codex", Command: "codex", Data: ".codex", Skills: ".codex/skills"},
	{Name: "cursor", Command: "cursor", Data: ".cursor", Skills: ".cursor/skills"},
	{Name: "antigravity", Command: "antigravity", Data: ".gemini/antigravity", Skills: ".gemini/antigravity/skills"},
	{Name: "opencode", Command: "opencode", Data: ".config/opencode", Skills: ".config/opencode/skills"},
	{Name: "pi", Command: "pi", Data: ".pi/agent", Skills: ".pi/agent/skills"},
}

// FindHost returns the table row called name.
func FindHost(name string) (Host, bool) {
	for _, h := range Hosts {
		if h.Name == name {
			return h, true
		}
	}
	return Host{}, false
}

// Detection says whether a host is present and why.
type Detection struct {
	Host    Host   `json:"host"`
	Present bool   `json:"present"`
	Why     string `json:"why,omitempty"`
}

// Detect reports a host present when its command is on PATH or its data folder exists.
func (e *Env) Detect() []Detection {
	out := make([]Detection, 0, len(Hosts))
	for _, h := range Hosts {
		d := Detection{Host: h}
		if p, err := exec.LookPath(h.Command); err == nil {
			d.Present, d.Why = true, "command "+p
		} else if fi, err := os.Stat(filepath.Join(e.Home, filepath.FromSlash(h.Data))); err == nil && fi.IsDir() {
			d.Present, d.Why = true, "folder ~/"+h.Data
		}
		out = append(out, d)
	}
	return out
}
