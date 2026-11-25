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

// Install puts the release tag of repo under <dataDir>/bin/<version>/, after verifying its SHA-256 against that
// tag's checksums.txt, and points <dataDir>/bin/current at the highest installed version. A version that is already
// there is not downloaded again. SAPULIDI_RELEASE_BASE and SAPULIDI_RAW_BASE replace the two download bases, which
// must be https.
func Install(ctx context.Context, client *http.Client, dataDir, repo, tag string) (Installed, error) {
	version, found := strings.CutPrefix(tag, "v")
	if _, ok := numbers(version); !found || !ok {
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
	if !executable(bin) {
		body, err := download(ctx, client, repo, tag, "sapulidi_"+version+"_"+runtime.GOOS+"_"+runtime.GOARCH)
		if err != nil {
			return Installed{}, err
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

// download returns the verified bytes of asset.
func download(ctx context.Context, client *http.Client, repo, tag, asset string) ([]byte, error) {
	base := os.Getenv("SAPULIDI_RELEASE_BASE")
	if base == "" {
		base = "https://github.com/" + repo + "/releases/download/" + tag
	}
	raw := os.Getenv("SAPULIDI_RAW_BASE")
	if raw == "" {
		raw = "https://raw.githubusercontent.com/" + repo + "/" + tag
	}
	for _, b := range []string{base, raw} {
		if !strings.HasPrefix(b, "https://") {
			return nil, fmt.Errorf("update: a download base must be https://: %s", b)
		}
	}
	sums, err := get(ctx, client, raw+"/checksums.txt", maxBody)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("update: no checksum line for %s in %s/checksums.txt", asset, raw)
	case len(want) > 1:
		return nil, fmt.Errorf("update: conflicting checksum lines for %s in %s/checksums.txt", asset, raw)
	}
	body, err := get(ctx, client, base+"/"+asset, maxBinary)
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != want[0] {
		return nil, fmt.Errorf("update: checksum mismatch for %s", asset)
	}
	return body, nil
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
// number over four dot parts, and is replaced by one rename.
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
		if !versionDir.MatchString(e.Name()) {
			continue
		}
		a, b := key(e.Name()), key(top)
		if top == "" || slices.Compare(a[:], b[:]) >= 0 {
			top = e.Name()
		}
	}
	if fi, err := os.Stat(filepath.Join(bindir, top, "sapulidi")); top == "" || err != nil || !fi.Mode().IsRegular() {
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
