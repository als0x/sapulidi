package install

import (
	"crypto/sha256"
	"encoding/hex"
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

// parseObjects reads a --json list: a top-level array, or an object holding the array under key. Anything else,
// an error document included, is an error, so a list that cannot be trusted never reads as "not installed".
func parseObjects(b []byte, key string) ([]map[string]any, error) {
	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err == nil {
		return arr, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, fmt.Errorf("cannot read the list from claude: %v", err)
	}
	raw, ok := obj[key]
	if !ok {
		return nil, fmt.Errorf("the list from claude has no %q", key)
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("cannot read %q from claude: %v", key, err)
	}
	return arr, nil
}

// parseList reads `claude plugin list --json`. A plugin is named by "id" (name@marketplace) or by "name" and "marketplace".
func parseList(b []byte) ([]listed, error) {
	raw, err := parseObjects(b, "plugins")
	if err != nil {
		return nil, err
	}
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	var out []listed
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

// sapulidiAll returns every installed plugin named sapulidi.
func (e *Env) sapulidiAll() ([]listed, error) {
	out, err := e.claude("plugin", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("claude plugin list --json: %v", err)
	}
	items, err := parseList(out)
	if err != nil {
		return nil, err
	}
	var all []listed
	for _, l := range items {
		if l.Name == "sapulidi" {
			all = append(all, l)
		}
	}
	return all, nil
}

// marketplaceListed says whether our marketplace is known to claude.
func (e *Env) marketplaceListed() (bool, error) {
	out, err := e.claude("plugin", "marketplace", "list", "--json")
	if err != nil {
		return false, fmt.Errorf("claude plugin marketplace list --json: %v", err)
	}
	items, err := parseObjects(out, "marketplaces")
	if err != nil {
		return false, err
	}
	for _, m := range items {
		if n, _ := m["name"].(string); n == Marketplace {
			return true, nil
		}
	}
	return false, nil
}

// ---- extraction ----

func fileMode(rel string) os.FileMode {
	if strings.HasPrefix(rel, "bin/") || strings.HasSuffix(rel, ".sh") {
		return 0o755
	}
	return 0o644
}

// copyFS writes every file of src under dst and returns what it wrote with the hash of the content; on an
// error it still returns the files written so far, so the caller can clean them up.
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
		sum := sha256.Sum256(b)
		files = append(files, FileRec{Path: path.Clean(p), SHA256: hex.EncodeToString(sum[:])})
		return os.Chmod(target, fileMode(p))
	})
	return files, err
}

// dirty lists what in an installed folder is not exactly what was installed: files changed since install and
// files nobody recorded. Nothing is deleted or changed here.
func dirty(en Entry) []string {
	rec := map[string]string{}
	for _, f := range en.Files {
		rec[f.Path] = f.SHA256
	}
	var out []string
	_ = filepath.WalkDir(en.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(en.Root, p)
		if rerr != nil {
			return nil
		}
		want, ok := rec[filepath.ToSlash(rel)]
		if !ok {
			out = append(out, p+" (not installed by sapulidi)")
		} else if got, herr := hashFile(p); herr != nil || got != want || !d.Type().IsRegular() {
			out = append(out, p+" (changed since install)")
		}
		return nil
	})
	return out
}

func (e *Env) persist(rec *Record, en Entry) error {
	rec.put(en)
	return e.save(*rec)
}

// extractPlugin replaces <data>/plugin with the embedded plugin. Nothing is deleted before the old tree is
// known to be exactly what was installed: the new tree is staged, the old folder is renamed aside, the new one
// renamed in, and only then is the old one removed (by removeRecorded). A failure leaves the old tree in place.
func (e *Env) extractPlugin(rec *Record, ver string) error {
	root := e.pluginDir()
	old, hadOld := rec.find("claude")
	if hadOld {
		if _, err := os.Lstat(root); err != nil {
			hadOld = false
		}
	}
	switch {
	case hadOld:
		if d := dirty(Entry{Root: root, Files: old.Files}); len(d) > 0 {
			return fmt.Errorf("%s was changed since install, nothing replaced: %s", root, strings.Join(d, "; "))
		}
	default:
		if _, err := os.Lstat(root); err == nil {
			return fmt.Errorf("%s exists and was not installed by sapulidi; move it away and run again", root)
		}
	}
	if err := os.MkdirAll(e.Data, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(e.Data, stagingPrefix)
	if err != nil {
		return err
	}
	cleanStage := func(files []FileRec) {
		_, notes := removeRecorded(e.Data, e.Home, Entry{Host: "claude", Root: stage, Files: files})
		for _, n := range notes {
			e.say("claude: %s", n)
		}
	}
	files, err := copyFS(e.Plugin, stage)
	if err == nil {
		err = os.Chmod(stage, 0o755)
	}
	if err != nil {
		cleanStage(files)
		return err
	}
	aside := ""
	if hadOld {
		aside = filepath.Join(e.Data, fmt.Sprintf("%s%d", asidePrefix, time.Now().UnixNano()))
		if err := os.Rename(root, aside); err != nil {
			cleanStage(files)
			return err
		}
	}
	if err := os.Rename(stage, root); err != nil {
		if aside != "" {
			_ = os.Rename(aside, root)
		}
		cleanStage(files)
		return err
	}
	if aside != "" {
		_, notes := removeRecorded(e.Data, e.Home, Entry{Host: "claude", Root: aside, Files: old.Files})
		for _, n := range notes {
			e.say("claude: %s", n)
		}
	}
	// the host steps already done stay recorded (the zero Entry when there was no record)
	en := Entry{Host: "claude", Root: root, Version: ver, Time: time.Now().UTC().Format(time.RFC3339), Files: files,
		AddedMarketplace: old.AddedMarketplace, InstalledPlugin: old.InstalledPlugin}
	if err := e.persist(rec, en); err != nil {
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

func failed(err error) Result { return Result{Status: "failed", Detail: err.Error()} }

func (e *Env) installClaude() Result {
	ver, err := e.embeddedVersion()
	if err != nil {
		return failed(err)
	}
	rec, err := e.load()
	if err != nil {
		return failed(err)
	}
	add := []string{"plugin", "marketplace", "add", e.pluginDir()}
	install := []string{"plugin", "install", PluginID}
	if _, err := exec.LookPath("claude"); err != nil {
		if err := e.extractPlugin(&rec, ver); err != nil {
			return failed(err)
		}
		return Result{Status: "not run", Detail: "claude is not on PATH; when it is, run:\n  claude " +
			strings.Join(add, " ") + "\n  claude " + strings.Join(install, " ")}
	}
	all, err := e.sapulidiAll()
	if err != nil {
		return failed(err)
	}
	var cur *listed
	for i := range all {
		if all[i].Marketplace != Marketplace {
			guide := "claude plugin uninstall sapulidi"
			from := "an unknown source"
			if all[i].Marketplace != "" {
				guide += "@" + all[i].Marketplace
				from = fmt.Sprintf("%q", all[i].Marketplace)
			}
			return Result{Status: "refused", Detail: fmt.Sprintf("a sapulidi plugin from %s is already installed and its hooks would run twice; "+
				"remove it first: %s", from, guide)}
		}
		if cur == nil {
			cur = &all[i]
		}
	}
	en, hadRec := rec.find("claude")
	byUs := func(owned bool) bool { return hadRec && owned }
	status := "installed"
	needAdd := false
	if cur != nil {
		if !byUs(en.InstalledPlugin) {
			return Result{Status: "already installed", Detail: fmt.Sprintf("%s (version %s) was installed by you, not by sapulidi: left alone and nothing recorded. "+
				"To let sapulidi manage it, run claude plugin uninstall %s and then sapulidi agents install claude", PluginID, cur.Version, PluginID)}
		}
		if cur.Version == ver {
			return Result{Status: "already installed", Detail: "version " + ver}
		}
		status = "upgraded"
	} else {
		present, err := e.marketplaceListed()
		if err != nil {
			return failed(err)
		}
		if present && !byUs(en.AddedMarketplace) {
			return Result{Status: "already installed", Detail: fmt.Sprintf("the marketplace %s was added by you, not by sapulidi: left alone and nothing recorded. "+
				"Run claude plugin install %s yourself, or remove the marketplace and run this again", Marketplace, PluginID)}
		}
		needAdd = !present
	}
	if err := e.extractPlugin(&rec, ver); err != nil {
		return failed(err)
	}
	en, _ = rec.find("claude")
	if needAdd {
		e.say("claude: %s", strings.Join(add, " "))
		if _, err := e.claude(add...); err != nil {
			// a duplicate add counts as success; anything else stops here
			if present, lerr := e.marketplaceListed(); lerr != nil || !present {
				return failed(fmt.Errorf("claude %s: %v", strings.Join(add, " "), err))
			}
		} else {
			en.AddedMarketplace = true
			if err := e.persist(&rec, en); err != nil {
				return failed(err)
			}
		}
	}
	e.say("claude: %s", strings.Join(install, " "))
	if _, err := e.claude(install...); err != nil {
		return failed(fmt.Errorf("claude %s: %v", strings.Join(install, " "), err))
	}
	en.InstalledPlugin = true
	if err := e.persist(&rec, en); err != nil {
		return failed(err)
	}
	return Result{Status: status, Detail: "version " + ver}
}

func (e *Env) installSkills(h Host) Result {
	skills := filepath.Join(e.Home, filepath.FromSlash(h.Skills))
	fi, err := os.Lstat(skills)
	if err != nil || (!fi.IsDir() && fi.Mode()&os.ModeSymlink == 0) {
		return Result{Status: "skipped", Detail: fmt.Sprintf("%s does not exist; create it and run again, or copy skills/sapulidi there yourself", skills)}
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return Result{Status: "refused", Detail: fmt.Sprintf("%s is a symlink; sapulidi never writes through one. Copy skills/sapulidi there yourself", skills)}
	}
	src, err := fs.Sub(e.Plugin, "skills/sapulidi")
	if err != nil {
		return failed(err)
	}
	rec, err := e.load()
	if err != nil {
		return failed(err)
	}
	dest := filepath.Join(skills, "sapulidi")
	if old, ok := rec.find(h.Name); ok {
		if _, err := os.Lstat(dest); err == nil {
			if d := dirty(Entry{Root: dest, Files: old.Files}); len(d) > 0 {
				return Result{Status: "refused", Detail: fmt.Sprintf("%s was changed since install, nothing replaced: %s", dest, strings.Join(d, "; "))}
			}
			if _, notes := removeRecorded(e.Data, e.Home, old); len(notes) > 0 {
				return Result{Status: "failed", Detail: strings.Join(notes, "; ")}
			}
		}
	}
	if _, err := os.Lstat(dest); err == nil {
		return Result{Status: "refused", Detail: fmt.Sprintf("%s already exists and was not installed by sapulidi; move it away and run again", dest)}
	}
	files, err := copyFS(src, dest)
	if err != nil {
		return failed(err)
	}
	ver, _ := e.embeddedVersion()
	if err := e.persist(&rec, Entry{Host: h.Name, Root: dest, Version: ver, Time: time.Now().UTC().Format(time.RFC3339), Files: files}); err != nil {
		return failed(err)
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
		ok, n := e.undoClaude(rec, &en)
		notes = append(notes, n...)
		if !ok {
			return Result{Status: "partial", Detail: "claude steps are left, so the record and the extracted folder are kept; fix that and run again", Notes: notes}
		}
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

// undoClaude undoes the host steps sapulidi ran, in reverse order, and nothing else. Each finished step is
// cleared in the record at once, so a rerun does only what is left. It returns false when a step is left.
func (e *Env) undoClaude(rec *Record, en *Entry) (bool, []string) {
	if !en.InstalledPlugin && !en.AddedMarketplace {
		return true, nil
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return false, []string{"claude is not on PATH; run: claude plugin uninstall " + PluginID + " and claude plugin marketplace remove " + Marketplace + ", or put claude on PATH and run this again"}
	}
	if en.InstalledPlugin {
		all, err := e.sapulidiAll()
		if err != nil {
			return false, []string{err.Error()}
		}
		for _, l := range all {
			if l.Marketplace == Marketplace {
				step := []string{"plugin", "uninstall", PluginID}
				e.say("claude: %s", strings.Join(step, " "))
				if _, err := e.claude(step...); err != nil {
					return false, []string{fmt.Sprintf("claude %s: %v", strings.Join(step, " "), err)}
				}
				break
			}
		}
		en.InstalledPlugin = false
		if err := e.persist(rec, *en); err != nil {
			return false, []string{err.Error()}
		}
	}
	if en.AddedMarketplace {
		step := []string{"plugin", "marketplace", "remove", Marketplace}
		e.say("claude: %s", strings.Join(step, " "))
		if _, err := e.claude(step...); err != nil {
			return false, []string{fmt.Sprintf("claude %s: %v", strings.Join(step, " "), err)}
		}
		en.AddedMarketplace = false
		if err := e.persist(rec, *en); err != nil {
			return false, []string{err.Error()}
		}
	}
	return true, nil
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
// through its CLI; the others are installed when the recorded copy exists. A record that cannot be read is
// returned as an error and shown on every row, never as "not installed".
func (e *Env) List() ([]Status, error) {
	rec, rerr := e.load()
	var out []Status
	for _, d := range e.Detect() {
		s := Status{Host: d.Host, Present: d.Present}
		if rerr != nil {
			s.Detail = "cannot read agents.json: " + rerr.Error()
		} else if d.Host.Name == "claude" {
			if _, err := exec.LookPath("claude"); err == nil {
				all, err := e.sapulidiAll()
				switch {
				case err != nil:
					s.Detail = err.Error()
				default:
					for _, l := range all {
						if l.Marketplace == Marketplace {
							s.Installed, s.Detail = true, "version "+l.Version
						} else if !s.Installed {
							s.Detail = "a sapulidi plugin from another source is installed"
						}
					}
				}
			}
		} else if en, ok := rec.find(d.Host.Name); ok {
			if fi, err := os.Lstat(en.Root); err == nil && fi.IsDir() {
				s.Installed = true
			}
		}
		out = append(out, s)
	}
	return out, rerr
}
