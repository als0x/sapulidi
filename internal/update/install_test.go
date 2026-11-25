package update

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

var testAsset = "sapulidi_0.4.0_" + runtime.GOOS + "_" + runtime.GOARCH

const newBinary = "the new binary\n"

// release serves /rel/<asset> and /raw/checksums.txt over TLS and records the paths asked for.
type release struct {
	assetStatus, sumsStatus int
	binary, sums            string
	asked                   []string
	server                  *httptest.Server
}

func sumLine(body, name string) string {
	return fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(body)), name)
}

func newRelease(t *testing.T) *release {
	t.Helper()
	r := &release{assetStatus: 200, sumsStatus: 200, binary: newBinary,
		sums: sumLine("another", "sapulidi_0.4.0_plan9_mips") + sumLine(newBinary, testAsset)}
	r.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		r.asked = append(r.asked, q.URL.Path)
		status, body := 404, ""
		switch q.URL.Path {
		case "/rel/" + testAsset:
			status, body = r.assetStatus, r.binary
		case "/raw/checksums.txt":
			status, body = r.sumsStatus, r.sums
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *release) bases() Bases {
	return Bases{Release: r.server.URL + "/rel", Raw: r.server.URL + "/raw"}
}

func (r *release) install(data, tag string) (Installed, error) {
	return Install(context.Background(), r.server.Client(), data, tag, r.bases())
}

// dataDir is a data directory with the given versions installed and current pointing at the last one.
func dataDir(t *testing.T, versions ...string) string {
	t.Helper()
	data := filepath.Join(t.TempDir(), "sapulidi")
	for _, v := range versions {
		dir := filepath.Join(data, "bin", v)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sapulidi"), []byte(v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if len(versions) > 0 {
		if err := os.Symlink(versions[len(versions)-1], filepath.Join(data, "bin", "current")); err != nil {
			t.Fatal(err)
		}
	}
	return data
}

// tree describes every entry under dir.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		v := fi.Mode().String()
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			v += " -> " + target
		case fi.Mode().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			v += " " + string(b)
		}
		out[p] = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func current(t *testing.T, data string) string {
	t.Helper()
	target, err := os.Readlink(filepath.Join(data, "bin", "current"))
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestInstallNewerTag(t *testing.T) {
	r := newRelease(t)
	data := dataDir(t, "0.3.0")
	got, err := r.install(data, "v0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if got != (Installed{Version: "0.4.0", Current: "0.4.0", Moved: true}) {
		t.Errorf("result %+v", got)
	}
	bin := filepath.Join(data, "bin", "0.4.0", "sapulidi")
	fi, err := os.Stat(bin)
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("binary: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(bin); string(b) != newBinary {
		t.Errorf("binary holds %q", b)
	}
	if c := current(t, data); c != "0.4.0" {
		t.Errorf("current -> %q", c)
	}
	if b, err := os.ReadFile(filepath.Join(data, "bin", "0.3.0", "sapulidi")); err != nil || string(b) != "0.3.0" {
		t.Errorf("old version: %q %v", b, err)
	}
	for dir, want := range map[string]int{"bin": 3, "bin/0.4.0": 1} { // no temporary file or link is left
		if entries, _ := os.ReadDir(filepath.Join(data, dir)); len(entries) != want {
			t.Errorf("%s holds %v", dir, entries)
		}
	}
}

func TestInstallIntoAnEmptyDataDirectory(t *testing.T) {
	r := newRelease(t)
	data := dataDir(t)
	if got, err := r.install(data, "v0.4.0"); err != nil || !got.Moved || current(t, data) != "0.4.0" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestInstallFailureChangesNothing(t *testing.T) {
	for name, c := range map[string]struct {
		set  func(*release)
		want string
	}{
		"mismatching checksum": {func(r *release) { r.binary = "tampered" }, "checksum mismatch"},
		"missing asset":        {func(r *release) { r.assetStatus = 404 }, "404"},
		"missing checksums":    {func(r *release) { r.sumsStatus = 404 }, "404"},
		"server error":         {func(r *release) { r.assetStatus = 500 }, "500"},
		"partial answer":       {func(r *release) { r.assetStatus = 206 }, "206"},
		"no checksum line":     {func(r *release) { r.sums = sumLine(newBinary, "other") }, "no checksum line"},
		"conflicting lines": {func(r *release) { r.sums += sumLine("x", testAsset) },
			"conflicting checksum lines"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRelease(t)
			c.set(r)
			data := dataDir(t, "0.3.0")
			before := tree(t, data)
			_, err := r.install(data, "v0.4.0")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v, want %q", err, c.want)
			}
			if after := tree(t, data); !reflect.DeepEqual(before, after) {
				t.Fatalf("changed:\n%v\n%v", before, after)
			}
		})
	}
}

// install.sh accepts CRLF checksum files and a repeated identical line.
func TestInstallReadsChecksumsAsInstallShDoes(t *testing.T) {
	r := newRelease(t)
	line := strings.TrimSuffix(sumLine(newBinary, testAsset), "\n") + "\r\n"
	r.sums = line + line
	if _, err := r.install(dataDir(t), "v0.4.0"); err != nil {
		t.Fatal(err)
	}
}

func TestFailedWriteLeavesNoFile(t *testing.T) {
	r := newRelease(t)
	data := dataDir(t, "0.3.0")
	dir := filepath.Join(data, "bin", "0.4.0")
	// A directory where the binary belongs: the final rename fails.
	if err := os.MkdirAll(filepath.Join(dir, "sapulidi"), 0o700); err != nil {
		t.Fatal(err)
	}
	before := tree(t, data)
	if _, err := r.install(data, "v0.4.0"); err == nil || !strings.Contains(err.Error(), "cannot install") {
		t.Fatalf("error %v", err)
	}
	if after := tree(t, data); !reflect.DeepEqual(before, after) {
		t.Fatalf("changed:\n%v\n%v", before, after)
	}
}

func TestCurrentOnlyMovesForward(t *testing.T) {
	r := newRelease(t)
	data := dataDir(t, "0.10.0")
	got, err := r.install(data, "v0.4.0")
	if err != nil || got.Moved || got.Current != "0.10.0" || current(t, data) != "0.10.0" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(data, "bin", "0.4.0", "sapulidi")); err != nil {
		t.Fatal(err)
	}
}

// Only a plain X.Y.Z directory counts: any other name is never made current, however high it sorts.
func TestCurrentIgnoresDirectoriesThatAreNotPlainVersions(t *testing.T) {
	r := newRelease(t)
	data := dataDir(t, "9", "9.0", "9.0.0.1", "09.0.0", "0.3.0")
	got, err := r.install(data, "v0.4.0")
	if err != nil || !got.Moved || current(t, data) != "0.4.0" {
		t.Fatalf("%+v current %s %v", got, current(t, data), err)
	}
}

// setBinary replaces the binary of an installed version.
func setBinary(t *testing.T, data, version, body string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(data, "bin", version, "sapulidi")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestInstalledVersionIsNotDownloadedAgain(t *testing.T) {
	r := newRelease(t)
	data := dataDir(t, "0.4.0", "0.3.0") // current is behind
	setBinary(t, data, "0.4.0", newBinary, 0o755)
	got, err := r.install(data, "v0.4.0")
	if err != nil || !reflect.DeepEqual(r.asked, []string{"/raw/checksums.txt"}) || !got.Moved ||
		current(t, data) != "0.4.0" {
		t.Fatalf("%+v asked %v %v", got, r.asked, err)
	}
	got, err = r.install(data, "v0.4.0")
	if err != nil || got.Moved || got.Current != "0.4.0" {
		t.Fatalf("again: %+v %v", got, err)
	}
}

func TestInstalledBinaryThatDoesNotMatchIsReplaced(t *testing.T) {
	r := newRelease(t)
	data := dataDir(t, "0.4.0", "0.3.0")
	got, err := r.install(data, "v0.4.0") // 0.4.0 holds something else
	if err != nil || !got.Moved {
		t.Fatalf("%+v %v", got, err)
	}
	b, err := os.ReadFile(filepath.Join(data, "bin", "0.4.0", "sapulidi"))
	if err != nil || string(b) != newBinary {
		t.Fatalf("binary holds %q %v", b, err)
	}
}

func TestInstalledBinaryIsNotTrustedWithoutAMatch(t *testing.T) {
	for name, set := range map[string]func(*release){
		"no checksums":  func(r *release) { r.sumsStatus = 404 },
		"no download":   func(r *release) { r.assetStatus = 404 },
		"bad download":  func(r *release) { r.binary = "tampered" },
		"no sums match": func(r *release) { r.sums = sumLine(newBinary, "other") },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRelease(t)
			set(r)
			data := dataDir(t, "0.4.0", "0.3.0")
			before := tree(t, data)
			if _, err := r.install(data, "v0.4.0"); err == nil {
				t.Fatal("accepted")
			}
			if after := tree(t, data); !reflect.DeepEqual(before, after) {
				t.Fatalf("changed:\n%v\n%v", before, after)
			}
		})
	}
}

func TestCurrentSkipsABinaryThatIsNotExecutable(t *testing.T) {
	r := newRelease(t)
	data := dataDir(t, "0.9.0", "0.3.0")
	setBinary(t, data, "0.9.0", "cut off", 0o600)
	r.binary = "tampered"
	if _, err := r.install(data, "v0.4.0"); err == nil || current(t, data) != "0.3.0" {
		t.Fatalf("failed install: current %s %v", current(t, data), err)
	}
	r.binary = newBinary
	got, err := r.install(data, "v0.4.0")
	if err != nil || !got.Moved || current(t, data) != "0.4.0" {
		t.Fatalf("%+v current %s %v", got, current(t, data), err)
	}
}

type recorder struct{ urls []string }

func (r *recorder) RoundTrip(q *http.Request) (*http.Response, error) {
	r.urls = append(r.urls, q.URL.String()+" "+q.Header.Get("User-Agent"))
	body := newBinary
	if strings.HasSuffix(q.URL.Path, "checksums.txt") {
		body = sumLine(newBinary, testAsset)
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Request: q}, nil
}

func TestInstallAsksTheURLsOfInstallSh(t *testing.T) {
	t.Setenv("SAPULIDI_RELEASE_BASE", "https://ignored.invalid") // the default bases read no variable
	r := &recorder{}
	_, err := Install(context.Background(), &http.Client{Transport: r}, dataDir(t), "v0.4.0",
		DefaultBases(testRepo, "v0.4.0"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://raw.githubusercontent.com/als0x/sapulidi/v0.4.0/checksums.txt sapulidi",
		"https://github.com/als0x/sapulidi/releases/download/v0.4.0/" + testAsset + " sapulidi",
	}
	if !reflect.DeepEqual(r.urls, want) {
		t.Fatalf("asked %q", r.urls)
	}
}

func TestInstallRefusesPlainHTTP(t *testing.T) {
	plain := serve(t, reply(200, newBinary))
	t.Run("base", func(t *testing.T) {
		r := newRelease(t)
		from := r.bases()
		from.Release = plain.URL
		_, err := Install(context.Background(), r.server.Client(), dataDir(t), "v0.4.0", from)
		if err == nil || !strings.Contains(err.Error(), "https") {
			t.Fatalf("error %v", err)
		}
		if len(r.asked) != 0 {
			t.Fatalf("asked %v", r.asked)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		r := newRelease(t)
		hop := httptest.NewTLSServer(http.RedirectHandler(plain.URL, http.StatusFound))
		t.Cleanup(hop.Close)
		from := r.bases()
		from.Release = hop.URL
		_, err := Install(context.Background(), r.server.Client(), dataDir(t), "v0.4.0", from)
		if err == nil || !strings.Contains(err.Error(), "https") {
			t.Fatalf("error %v", err)
		}
	})
}

func TestBasesFromEnv(t *testing.T) {
	def := DefaultBases(testRepo, "v0.4.0")
	t.Setenv("SAPULIDI_RELEASE_BASE", "")
	t.Setenv("SAPULIDI_RAW_BASE", "")
	if got := def.FromEnv(); got != def {
		t.Fatalf("empty variables: %+v", got)
	}
	t.Setenv("SAPULIDI_RELEASE_BASE", "https://r.invalid")
	t.Setenv("SAPULIDI_RAW_BASE", "https://s.invalid")
	if got := def.FromEnv(); got != (Bases{Release: "https://r.invalid", Raw: "https://s.invalid"}) {
		t.Fatalf("%+v", got)
	}
}

func TestInstallRefusesATagThatIsNotARelease(t *testing.T) {
	r := newRelease(t)
	for _, tag := range []string{"", "0.4.0", "v0.4", "v0.4.0-rc1", "v../../x",
		"v00.4.0", "v0.04.0", "v0.4.00", "v01.2.3", "v1.02.3", "v1.2.03"} {
		if _, err := r.install(dataDir(t), tag); err == nil {
			t.Errorf("%q accepted", tag)
		}
	}
	if len(r.asked) != 0 {
		t.Fatalf("asked %v", r.asked)
	}
}

func TestInstallRefusesSymlinkedDirectories(t *testing.T) {
	for _, link := range []string{"bin", "bin/0.4.0"} {
		r := newRelease(t)
		data, elsewhere := dataDir(t), t.TempDir()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(data, link)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(data, link)); err != nil {
			t.Fatal(err)
		}
		if _, err := r.install(data, "v0.4.0"); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Errorf("%s: error %v", link, err)
		}
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Errorf("%s: wrote through the link: %v", link, entries)
		}
	}
}
