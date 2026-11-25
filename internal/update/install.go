package update

// The download and the checksum rules here are those of install.sh; a change to one belongs in the other. Nothing
// is written before the checksum matches, and no old version is removed.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/state/atomic"
)

const (
	// maxBinary is the most of a release binary that is read.
	maxBinary = 200 << 20
	// downloadTimeout bounds each download.
	downloadTimeout = 5 * time.Minute
)

// Installed is what Install left behind: the version asked for, the version current points at, and whether this
// call moved current to the version asked for.
type Installed struct {
	Version string
	Current string
	Moved   bool
}

// Bases are where one release is downloaded from: the directory of its assets and that of its checksums.txt.
type Bases struct{ Release, Raw string }

// DefaultBases are the URLs of install.sh for the release tag of repo.
func DefaultBases(repo, tag string) Bases {
	return Bases{
		Release: "https://github.com/" + repo + "/releases/download/" + tag,
		Raw:     "https://raw.githubusercontent.com/" + repo + "/" + tag,
	}
}

// FromEnv replaces each base by SAPULIDI_RELEASE_BASE or SAPULIDI_RAW_BASE when that is set, as install.sh does.
// Only an update a person asked for may use it.
func (b Bases) FromEnv() Bases {
	if v := os.Getenv("SAPULIDI_RELEASE_BASE"); v != "" {
		b.Release = v
	}
	if v := os.Getenv("SAPULIDI_RAW_BASE"); v != "" {
		b.Raw = v
	}
	return b
}

// Install puts the release tag under <dataDir>/bin/<version>/, after verifying its SHA-256 against that tag's
// checksums.txt, and points <dataDir>/bin/current at the highest installed version. A binary that is already there
// is kept only when it matches the checksum too. The bases must be https.
func Install(ctx context.Context, client *http.Client, dataDir, tag string, from Bases) (Installed, error) {
	version, found := strings.CutPrefix(tag, "v")
	if !found || !plainVersion(version) {
		return Installed{}, fmt.Errorf("update: %q is not a release tag", tag)
	}
	if !filepath.IsAbs(dataDir) {
		return Installed{}, fmt.Errorf("update: data directory must be an absolute path: %s", dataDir)
	}
	bindir := filepath.Join(dataDir, "bin")
	dir := filepath.Join(bindir, version)
	bin := filepath.Join(dir, "sapulidi")
	for _, d := range []string{bindir, dir} {
		if fi, err := os.Lstat(d); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return Installed{}, fmt.Errorf("update: refusing symlinked directory %s", d)
		}
	}
	asset := "sapulidi_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH
	want, err := checksum(ctx, client, from, asset)
	if err != nil {
		return Installed{}, err
	}
	if !executable(bin) || !matches(bin, want) {
		body, err := get(ctx, client, from.Release+"/"+asset, maxBinary)
		if err != nil {
			return Installed{}, err
		}
		if sum(body) != want {
			return Installed{}, fmt.Errorf("update: checksum mismatch for %s", asset)
		}
		// One rename puts the file there with mode 0600; until the chmod it does not count as installed, so a run
		// cut off here downloads it again.
		err = atomic.WriteFile(bin, body)
		if err == nil {
			err = os.Chmod(bin, 0o755)
		}
		if err != nil {
			return Installed{}, fmt.Errorf("update: cannot install %s: %w", bin, err)
		}
	}
	before, _ := os.Readlink(filepath.Join(bindir, "current"))
	if err := updateCurrent(bindir); err != nil {
		return Installed{}, err
	}
	after, err := os.Readlink(filepath.Join(bindir, "current"))
	if err != nil || !executable(filepath.Join(bindir, "current", "sapulidi")) {
		return Installed{}, fmt.Errorf("update: %s/current/sapulidi is not an executable file", bindir)
	}
	return Installed{Version: version, Current: after, Moved: after != before && after == version}, nil
}

func executable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

// plainVersion reports "MAJOR.MINOR.PATCH" with no leading zero, so that one release has one directory name.
func plainVersion(s string) bool {
	n, ok := numbers(s)
	return ok && s == fmt.Sprintf("%d.%d.%d", n[0], n[1], n[2])
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// matches reports whether the file at path has the SHA-256 want. A file that cannot be read does not.
func matches(path, want string) bool {
	b, err := os.ReadFile(path)
	return err == nil && sum(b) == want
}

// checksum is the SHA-256 that checksums.txt gives for asset.
func checksum(ctx context.Context, client *http.Client, from Bases, asset string) (string, error) {
	for _, b := range []string{from.Release, from.Raw} {
		if !strings.HasPrefix(b, "https://") {
			return "", fmt.Errorf("update: a download base must be https://: %s", b)
		}
	}
	sums, err := get(ctx, client, from.Raw+"/checksums.txt", maxBody)
	if err != nil {
		return "", err
	}
	var want []string
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[1] == asset {
			want = append(want, f[0])
		}
	}
	slices.Sort(want)
	switch want = slices.Compact(want); {
	case len(want) == 0:
		return "", fmt.Errorf("update: no checksum line for %s in %s/checksums.txt", asset, from.Raw)
	case len(want) > 1:
		return "", fmt.Errorf("update: conflicting checksum lines for %s in %s/checksums.txt", asset, from.Raw)
	}
	return want[0], nil
}

// get fetches url, following redirects to https only. Anything but a 200 of at most limit bytes is an error.
func get(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "sapulidi")
	c := *client
	c.Jar = nil
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		switch {
		case r.URL.Scheme != "https":
			return errors.New("redirect away from https refused")
		case len(via) >= 10:
			return errors.New("too many redirects")
		}
		return nil
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: download failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: download failed: %s answered %d", url, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("update: download failed: %s: %w", url, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("update: download failed: %s is longer than %d bytes", url, limit)
	}
	return b, nil
}

var versionDir = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)

// updateCurrent is update_current of install.sh: current points at the highest installed version, compared number by
// number over four dot parts, and is replaced by one rename. Unlike there, a version counts only when its binary
// is executable, so one that was cut off before its chmod is passed over.
func updateCurrent(bindir string) error {
	entries, err := os.ReadDir(bindir)
	if err != nil {
		return err
	}
	key := func(name string) (k [4]uint64) {
		for i, p := range strings.Split(name, ".") {
			if i < len(k) {
				k[i], _ = strconv.ParseUint(p, 10, 64)
			}
		}
		return k
	}
	top := ""
	for _, e := range entries {
		if !versionDir.MatchString(e.Name()) || !executable(filepath.Join(bindir, e.Name(), "sapulidi")) {
			continue
		}
		a, b := key(e.Name()), key(top)
		if top == "" || slices.Compare(a[:], b[:]) >= 0 {
			top = e.Name()
		}
	}
	if top == "" {
		return nil
	}
	link := filepath.Join(bindir, "current")
	if target, err := os.Readlink(link); err == nil && target == top {
		return nil
	}
	next := filepath.Join(bindir, fmt.Sprintf(".current.%d.%d", os.Getpid(), time.Now().UnixNano()))
	if err := os.Symlink(top, next); err != nil {
		return fmt.Errorf("update: cannot move current: %w", err)
	}
	if err := os.Rename(next, link); err != nil {
		return fmt.Errorf("update: cannot move current: %w", err)
	}
	return nil
}
