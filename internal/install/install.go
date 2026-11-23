package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Marketplace is the marketplace name in .claude-plugin/marketplace.json.
	Marketplace = "als0x-sapulidi"
	// PluginID is the plugin as Claude Code names it.
	PluginID = "sapulidi@" + Marketplace
)

// Env is everything the install code needs from the machine. Home and Data are absolute; Plugin is the
// embedded plugin tree; Log, when set, receives one line per step.
type Env struct {
	Home   string
	Data   string
	Plugin fs.FS
	Log    func(string)
}

// Result is the outcome for one host. Status is one of: installed, upgraded, already installed, not run,
// skipped, refused, failed, removed, partial, nothing recorded.
type Result struct {
	Host   string   `json:"host"`
	Status string   `json:"status"`
	Detail string   `json:"detail,omitempty"`
	Tested bool     `json:"tested"`
	Notes  []string `json:"notes,omitempty"`
}

// Failed reports a status that makes the command exit 1.
func (r Result) Failed() bool {
	return r.Status == "refused" || r.Status == "failed" || r.Status == "partial"
}

func (e *Env) say(format string, a ...any) {
	if e.Log != nil {
		e.Log(fmt.Sprintf(format, a...))
	}
}

func (e *Env) pluginDir() string { return filepath.Join(e.Data, "plugin") }

func (e *Env) embeddedVersion() (string, error) {
	b, err := fs.ReadFile(e.Plugin, ".claude-plugin/plugin.json")
	if err != nil {
		return "", err
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &m); err != nil || m.Version == "" {
		return "", errors.New("embedded plugin.json has no version")
	}
	return m.Version, nil
}

// ---- the claude CLI ----

type listed struct {
	Name, Marketplace, Version string
}

func (e *Env) claude(args ...string) ([]byte, error) {
	cmd := exec.Command("claude", args...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
	}
	return out, err
}

// parseList reads `claude plugin list --json`: an array of plugins, or an object with a "plugins" array. A plugin
// is named by "id" (name@marketplace) or by "name" and "marketplace".
func parseList(b []byte) ([]listed, error) {
	var raw []map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		var obj struct {
			Plugins []map[string]any `json:"plugins"`
		}
		if err2 := json.Unmarshal(b, &obj); err2 != nil {
			return nil, fmt.Errorf("cannot read the plugin list: %v", err)
		}
		raw = obj.Plugins
	}
	var out []listed
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	for _, m := range raw {
		l := listed{Name: str(m, "name"), Marketplace: str(m, "marketplace"), Version: str(m, "version")}
		if id := str(m, "id"); id != "" {
			if n, mk, ok := strings.Cut(id, "@"); ok {
				l.Name, l.Marketplace = n, mk
			} else if l.Name == "" {
				l.Name = id
			}
		}
		out = append(out, l)
	}
	return out, nil
}

// sapulidiListed returns the installed sapulidi plugin, if any.
func (e *Env) sapulidiListed() (*listed, error) {
	out, err := e.claude("plugin", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("claude plugin list --json: %v", err)
	}
	items, err := parseList(out)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Name == "sapulidi" {
			return &items[i], nil
		}
	}
	return nil, nil
}

// ---- extraction ----

func fileMode(rel string) os.FileMode {
	if strings.HasPrefix(rel, "bin/") || strings.HasSuffix(rel, ".sh") {
		return 0o755
	}
	return 0o644
}

// copyFS writes every file of src under dst and returns what it wrote with the hash of the content.
func copyFS(src fs.FS, dst string) ([]FileRec, error) {
	var files []FileRec
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, err
	}
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, b, fileMode(p)); err != nil {
			return err
		}
		if err := os.Chmod(target, fileMode(p)); err != nil {
			return err
		}
		sum, err := hashFile(target)
		if err != nil {
			return err
		}
		files = append(files, FileRec{Path: path.Clean(p), SHA256: sum})
		return nil
	})
	return files, err
}

// clearOld removes what an earlier install of host recorded (hash-checked) and reports what it kept.
func (e *Env) clearOld(rec *Record, host string) {
	if old, ok := rec.find(host); ok {
		_, notes := removeRecorded(e.Data, e.Home, old)
		for _, n := range notes {
			e.say("%s: %s", host, n)
		}
	}
}

// extractPlugin replaces <data>/plugin with the embedded plugin: the new tree is staged next to it and renamed in.
func (e *Env) extractPlugin(rec *Record, ver string) error {
	e.clearOld(rec, "claude")
	root := e.pluginDir()
	if _, err := os.Lstat(root); err == nil {
		return fmt.Errorf("%s exists with files that sapulidi did not install or that changed; move it away and run again", root)
	}
	if err := os.MkdirAll(e.Data, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(e.Data, "plugin-staging-")
	if err != nil {
		return err
	}
	files, err := copyFS(e.Plugin, stage)
	if err == nil {
		err = os.Chmod(stage, 0o755)
	}
	if err == nil {
		err = os.Rename(stage, root)
	}
	if err != nil {
		return fmt.Errorf("%v (partial copy left in %s)", err, stage)
	}
	rec.put(Entry{Host: "claude", Root: root, Version: ver, Time: time.Now().UTC().Format(time.RFC3339), Files: files})
	if err := e.save(*rec); err != nil {
		return err
	}
	e.say("claude: extracted the plugin to %s", root)
	return nil
}

// ---- install ----

// Install installs into one host by name.
func (e *Env) Install(name string) ([]Result, error) {
	h, ok := FindHost(name)
	if !ok {
		return nil, fmt.Errorf("unknown host %q", name)
	}
	var r Result
	if h.Name == "claude" {
		r = e.installClaude()
	} else {
		r = e.installSkills(h)
	}
	r.Host, r.Tested = h.Name, h.Tested
	e.say("%s: %s%s", h.Name, r.Status, untested(h))
	return []Result{r}, nil
}

func untested(h Host) string {
	if h.Tested {
		return ""
	}
	return " (untested)"
}

// InstallAll installs into every detected host and lists the skipped ones with the reason.
func (e *Env) InstallAll() []Result {
	var out []Result
	for _, d := range e.Detect() {
		if !d.Present {
			e.say("%s: skipped%s", d.Host.Name, untested(d.Host))
			out = append(out, Result{Host: d.Host.Name, Status: "skipped", Detail: "not detected", Tested: d.Host.Tested})
			continue
		}
		r, _ := e.Install(d.Host.Name)
		out = append(out, r...)
	}
	return out
}

func (e *Env) installClaude() Result {
	ver, err := e.embeddedVersion()
	if err != nil {
		return Result{Status: "failed", Detail: err.Error()}
	}
	rec, err := e.load()
	if err != nil {
		return Result{Status: "failed", Detail: err.Error()}
	}
	add := []string{"plugin", "marketplace", "add", e.pluginDir()}
	install := []string{"plugin", "install", PluginID}
	if _, err := exec.LookPath("claude"); err != nil {
		if err := e.extractPlugin(&rec, ver); err != nil {
			return Result{Status: "failed", Detail: err.Error()}
		}
		return Result{Status: "not run", Detail: "claude is not on PATH; when it is, run:\n  claude " +
			strings.Join(add, " ") + "\n  claude " + strings.Join(install, " ")}
	}
	cur, err := e.sapulidiListed()
	if err != nil {
		return Result{Status: "failed", Detail: err.Error()}
	}
	status := "installed"
	if cur != nil {
		if cur.Marketplace != Marketplace {
			return Result{Status: "refused", Detail: fmt.Sprintf("a sapulidi plugin from %q is already installed and its hooks would run twice; "+
				"remove it first: claude plugin uninstall sapulidi@%s", cur.Marketplace, cur.Marketplace)}
		}
		if cur.Version == ver {
			return Result{Status: "already installed", Detail: "version " + ver}
		}
		status = "upgraded"
	}
	if err := e.extractPlugin(&rec, ver); err != nil {
		return Result{Status: "failed", Detail: err.Error()}
	}
	steps := [][]string{install}
	if cur == nil {
		steps = [][]string{add, install}
	}
	for _, s := range steps {
		e.say("claude: %s", strings.Join(s, " "))
		if _, err := e.claude(s...); err != nil {
			return Result{Status: "failed", Detail: fmt.Sprintf("claude %s: %v", strings.Join(s, " "), err)}
		}
	}
	return Result{Status: status, Detail: "version " + ver}
}

func (e *Env) installSkills(h Host) Result {
	skills := filepath.Join(e.Home, filepath.FromSlash(h.Skills))
	if fi, err := os.Stat(skills); err != nil || !fi.IsDir() {
		return Result{Status: "skipped", Detail: fmt.Sprintf("%s does not exist; create it and run again, or copy skills/sapulidi there yourself", skills)}
	}
	src, err := fs.Sub(e.Plugin, "skills/sapulidi")
	if err != nil {
		return Result{Status: "failed", Detail: err.Error()}
	}
	rec, err := e.load()
	if err != nil {
		return Result{Status: "failed", Detail: err.Error()}
	}
	e.clearOld(&rec, h.Name)
	dest := filepath.Join(skills, "sapulidi")
	if _, err := os.Lstat(dest); err == nil {
		return Result{Status: "refused", Detail: fmt.Sprintf("%s already exists and was not installed by sapulidi (or changed); move it away and run again", dest)}
	}
	files, err := copyFS(src, dest)
	if err != nil {
		return Result{Status: "failed", Detail: err.Error()}
	}
	ver, _ := e.embeddedVersion()
	rec.put(Entry{Host: h.Name, Root: dest, Version: ver, Time: time.Now().UTC().Format(time.RFC3339), Files: files})
	if err := e.save(rec); err != nil {
		return Result{Status: "failed", Detail: err.Error()}
	}
	return Result{Status: "installed", Detail: "copied the skill to " + dest}
}

// ---- remove ----

// Remove removes what an earlier install recorded for one host, or for all of them.
func (e *Env) Remove(name string) ([]Result, error) {
	rec, err := e.load()
	if err != nil {
		return nil, err
	}
	var hosts []string
	if name == "all" {
		for _, en := range rec.Entries {
			hosts = append(hosts, en.Host)
		}
	} else if _, ok := FindHost(name); ok {
		hosts = []string{name}
	} else {
		return nil, fmt.Errorf("unknown host %q", name)
	}
	var out []Result
	for _, hn := range hosts {
		h, _ := FindHost(hn)
		en, ok := rec.find(hn)
		var r Result
		if !ok {
			r = Result{Status: "nothing recorded", Detail: "sapulidi did not install into " + hn}
		} else {
			r = e.removeHost(&rec, en)
		}
		r.Host, r.Tested = hn, h.Tested
		e.say("%s: %s", hn, r.Status)
		out = append(out, r)
	}
	return out, nil
}

func (e *Env) removeHost(rec *Record, en Entry) Result {
	var notes []string
	if en.Host == "claude" {
		notes = append(notes, e.uninstallClaude()...)
	}
	n, more := removeRecorded(e.Data, e.Home, en)
	notes = append(notes, more...)
	var left []FileRec
	for _, f := range en.Files {
		if _, err := os.Lstat(filepath.Join(en.Root, filepath.FromSlash(f.Path))); err == nil {
			left = append(left, f)
		}
	}
	status := "removed"
	if len(left) == 0 {
		rec.drop(en.Host)
	} else {
		en.Files = left
		rec.put(en)
		status = "partial"
	}
	if err := e.save(*rec); err != nil {
		notes = append(notes, err.Error())
		status = "failed"
	}
	return Result{Status: status, Detail: fmt.Sprintf("%d files removed", n), Notes: notes}
}

// uninstallClaude runs the two host commands that undo the install and returns a line for each problem.
func (e *Env) uninstallClaude() []string {
	if _, err := exec.LookPath("claude"); err != nil {
		return []string{"claude is not on PATH; run: claude plugin uninstall " + PluginID + " and claude plugin marketplace remove " + Marketplace}
	}
	var notes []string
	cur, err := e.sapulidiListed()
	if err != nil {
		notes = append(notes, err.Error())
	}
	var steps [][]string
	if cur != nil && cur.Marketplace == Marketplace {
		steps = append(steps, []string{"plugin", "uninstall", PluginID})
	}
	steps = append(steps, []string{"plugin", "marketplace", "remove", Marketplace})
	for _, s := range steps {
		e.say("claude: %s", strings.Join(s, " "))
		if _, err := e.claude(s...); err != nil {
			notes = append(notes, fmt.Sprintf("claude %s: %v", strings.Join(s, " "), err))
		}
	}
	return notes
}

// ---- list ----

// Status is one row of 'sapulidi agents'.
type Status struct {
	Host      Host   `json:"host"`
	Present   bool   `json:"present"`
	Installed bool   `json:"installed"`
	Detail    string `json:"detail,omitempty"`
}

// List reports every host: whether it is present and whether sapulidi is installed in it. Claude is asked
// through its CLI; the others are installed when the recorded copy exists.
func (e *Env) List() []Status {
	rec, _ := e.load()
	var out []Status
	for _, d := range e.Detect() {
		s := Status{Host: d.Host, Present: d.Present}
		if d.Host.Name == "claude" {
			if _, err := exec.LookPath("claude"); err == nil {
				cur, err := e.sapulidiListed()
				switch {
				case err != nil:
					s.Detail = err.Error()
				case cur != nil && cur.Marketplace == Marketplace:
					s.Installed, s.Detail = true, "version "+cur.Version
				case cur != nil:
					s.Detail = "a sapulidi plugin from " + cur.Marketplace + " is installed"
				}
			}
		} else if en, ok := rec.find(d.Host.Name); ok {
			if fi, err := os.Lstat(en.Root); err == nil && fi.IsDir() {
				s.Installed = true
			}
		}
		out = append(out, s)
	}
	return out
}
