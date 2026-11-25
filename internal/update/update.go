// Package update learns whether a newer release exists: one request to the release API, a cached answer in the
// state directory, the install routes recorded there and the one-line notice. It updates nothing. It is the only
// package that makes network calls, and a request carries nothing that identifies the user or the machine.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/state/atomic"
	"github.com/als0x/sapulidi/internal/version"
)

// APIBase is the release API.
const APIBase = "https://api.github.com"

// maxBody is the most of a release answer that is read; a longer one is an error.
const maxBody = 64 << 10

// width is the number of columns a notice stays under.
const width = 80

// Cache is what the last successful check learned: the latest tag, and when.
type Cache struct {
	Tag       string    `json:"tag"`
	CheckedAt time.Time `json:"checked_at"`
}

func cachePath(stateDir string) string { return filepath.Join(stateDir, "update.json") }

// LoadCache reads <state>/update.json. A missing file is an empty cache.
func LoadCache(stateDir string) (Cache, error) {
	b, err := os.ReadFile(cachePath(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return Cache{}, nil
	}
	if err != nil {
		return Cache{}, err
	}
	var c Cache
	if err := json.Unmarshal(b, &c); err != nil {
		return Cache{}, fmt.Errorf("update: %s: %w", cachePath(stateDir), err)
	}
	return c, nil
}

// SaveCache writes <state>/update.json atomically with mode 0600.
func SaveCache(stateDir string, c Cache) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return atomic.WriteFile(cachePath(stateDir), append(b, '\n'))
}

// Every is how often at most a check is started.
const Every = 24 * time.Hour

// Recent reports whether t is within Every of now. A time further ahead than that (a clock that was wrong) is not.
func Recent(t time.Time) bool {
	age := time.Since(t)
	return age < Every && age > -Every
}

// Claim takes the one slot per Every for starting a check: of all the processes asking at once, one wins. The
// slot is the file <state>/update.claim and its modification time. The first claim is an exclusive create. An old
// one is replaced by renaming <state>/update.claim.next over it, and only the process that created that file may.
func Claim(stateDir string) (bool, error) {
	claim := filepath.Join(stateDir, "update.claim")
	err := createNew(claim)
	if !errors.Is(err, fs.ErrExist) {
		return err == nil, err
	}
	if old, err := isOld(claim); !old {
		return false, err
	}
	next := claim + ".next"
	if err := createNew(next); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return false, err
		}
		// Another process is replacing the claim, or one died doing so long ago: its file is then moved out of
		// the way, with its old time, and a later call replaces the claim as usual.
		if old, _ := isOld(next); old {
			return false, os.Rename(next, claim)
		}
		return false, nil
	}
	old, err := isOld(claim) // again: the holder before this one may just have replaced it
	if err != nil {
		return false, err
	}
	if err := os.Rename(next, claim); err != nil {
		return false, err
	}
	return old, nil
}

func createNew(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

func isOld(path string) (bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	return !Recent(fi.ModTime()), nil
}

// Fetch asks apiBase for the latest release of repo and returns its tag. It sends one GET: no authentication, no
// query string, and no redirect is followed. Anything but a 200 with a JSON document naming a tag is an error.
func Fetch(ctx context.Context, client *http.Client, apiBase, repo string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "sapulidi")
	req.Header.Set("Accept", "application/vnd.github+json")
	once := *client
	once.Jar = nil
	once.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := once.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update: the release API answered %s", res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxBody {
		return "", fmt.Errorf("update: the release answer is longer than %d bytes", maxBody)
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(b, &release); err != nil {
		return "", fmt.Errorf("update: the release answer is not JSON: %w", err)
	}
	if release.Tag == "" {
		return "", errors.New("update: the release answer names no tag")
	}
	return release.Tag, nil
}

// numbers parses "MAJOR.MINOR.PATCH", digits only.
func numbers(s string) (n [3]uint64, ok bool) {
	parts := strings.Split(s, ".")
	if len(parts) != len(n) {
		return n, false
	}
	for i, p := range parts {
		v, err := strconv.ParseUint(p, 10, 64) // accepts no sign, space or suffix
		if err != nil {
			return n, false
		}
		n[i] = v
	}
	return n, true
}

// Newer reports whether the release tag "vX.Y.Z" is above current ("X.Y.Z", with or without the v: release builds
// carry it without). A tag or version of any other form, a pre-release included, is never newer.
func Newer(tag, current string) bool {
	rest, found := strings.CutPrefix(tag, "v")
	t, tok := numbers(rest)
	c, cok := numbers(strings.TrimPrefix(current, "v"))
	if !found || !tok || !cok {
		return false
	}
	for i := range t {
		if t[i] != c[i] {
			return t[i] > c[i]
		}
	}
	return false
}

// Route is one way this copy was installed.
type Route string

const (
	RoutePlugin    Route = "plugin"
	RouteInstallSh Route = "install.sh"
)

// Routes lists the routes whose marker file <state>/installed-by/<route> exists: the launcher and install.sh
// write them.
func Routes(stateDir string) []Route {
	var out []Route
	for _, r := range []Route{RoutePlugin, RouteInstallSh} {
		if _, err := os.Stat(filepath.Join(stateDir, "installed-by", string(r))); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// Notice is the line that names a newer release and how to get it, or "" when the cached tag is not newer than
// current. marketplace is the plugin's marketplace on a host that has the /plugin command; "" for any other host.
// The longest wording under 80 columns is used; when none fits, the line points at `sapulidi version` instead of
// giving the command.
func Notice(current string, c Cache, routes []Route, marketplace string) string {
	if version.Repo == "" || !Newer(c.Tag, current) {
		return ""
	}
	var cmds []string
	for _, r := range routes {
		switch {
		case r == RouteInstallSh:
			cmds = append(cmds, "sapulidi update")
		case r == RoutePlugin && marketplace == "":
			cmds = append(cmds, "update the plugin in your agent tool")
		case r == RoutePlugin:
			cmds = append(cmds, "/plugin update sapulidi@"+marketplace)
		}
	}
	command := strings.Join(cmds, " or ")
	if command == "" {
		command = "see https://github.com/" + version.Repo + "/releases"
	}
	for _, line := range []string{
		fmt.Sprintf("Sapu Lidi %s is out (you have %s). Update: %s", c.Tag, current, command),
		fmt.Sprintf("Sapu Lidi %s is out (you have %s): %s", c.Tag, current, command),
		fmt.Sprintf("Sapu Lidi %s (you have %s): %s", c.Tag, current, command),
		fmt.Sprintf("Sapu Lidi %s: %s", c.Tag, command),
	} {
		if len(line) < width {
			return line
		}
	}
	const short = "Sapu Lidi %s is out: run sapulidi version"
	room := width - 1 - len(fmt.Sprintf(short, ""))
	return fmt.Sprintf(short, c.Tag[:min(len(c.Tag), room)]) // a newer tag is ASCII: "v", digits and dots
}
