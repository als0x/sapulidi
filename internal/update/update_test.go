package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/version"
)

const testRepo = "als0x/sapulidi"

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func reply(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestFetchReturnsTagAndSendsNothingIdentifying(t *testing.T) {
	var got []*http.Request
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Clone(context.Background()))
		_, _ = w.Write([]byte(`{"tag_name":"v0.4.0","name":"x"}`))
	})
	tag, err := Fetch(context.Background(), s.Client(), s.URL, testRepo)
	if err != nil || tag != "v0.4.0" {
		t.Fatalf("tag %q err %v", tag, err)
	}
	if len(got) != 1 {
		t.Fatalf("%d requests", len(got))
	}
	r := got[0]
	if r.Method != http.MethodGet || r.URL.Path != "/repos/als0x/sapulidi/releases/latest" || r.URL.RawQuery != "" {
		t.Errorf("request %s %s", r.Method, r.URL)
	}
	if r.Header.Get("User-Agent") != "sapulidi" || r.Header.Get("Accept") != "application/vnd.github+json" {
		t.Errorf("headers %v", r.Header)
	}
	for _, h := range []string{"Authorization", "Cookie"} {
		if r.Header.Get(h) != "" {
			t.Errorf("header %s sent", h)
		}
	}
}

func TestFetchErrors(t *testing.T) {
	big := `{"tag_name":"v0.4.0","body":"` + strings.Repeat("a", 64<<10) + `"}`
	for name, h := range map[string]http.HandlerFunc{
		"forbidden":    reply(403, `{"tag_name":"v0.4.0"}`),
		"rate limited": reply(429, `{"tag_name":"v0.4.0"}`),
		"server error": reply(500, ""),
		"not json":     reply(200, "<html>"),
		"truncated":    reply(200, `{"tag_name":"v0.4.0"`),
		"trailing":     reply(200, `{"tag_name":"v0.4.0"} x`),
		"empty tag":    reply(200, `{"tag_name":""}`),
		"no tag":       reply(200, `{}`),
		"over 64 KB":   reply(200, big),
		// JSON that is complete before the limit: only the size makes it an error.
		"padded over 64 KB": reply(200, `{"tag_name":"v0.4.0"}`+strings.Repeat(" ", 64<<10)),
	} {
		t.Run(name, func(t *testing.T) {
			s := serve(t, h)
			if tag, err := Fetch(context.Background(), s.Client(), s.URL, testRepo); err == nil || tag != "" {
				t.Fatalf("tag %q err %v", tag, err)
			}
		})
	}
}

func TestFetchDoesNotFollowRedirects(t *testing.T) {
	followed := false
	other := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		followed = true
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9"}`))
	})
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusMovedPermanently)
	})
	if tag, err := Fetch(context.Background(), s.Client(), s.URL, testRepo); err == nil || followed {
		t.Fatalf("tag %q err %v followed %v", tag, err, followed)
	}
}

func TestFetchStopsAtTheDeadline(t *testing.T) {
	release := make(chan struct{})
	s := serve(t, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Fetch(ctx, s.Client(), s.URL, testRepo); err == nil {
		t.Fatal("no error from a hanging server")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		tag, current string
		want         bool
	}{
		{"v0.3.0", "0.3.0", false},
		{"v0.3.0", "v0.3.0", false},
		{"v0.2.9", "0.3.0", false},
		{"v0.3.1", "0.3.0", true},
		{"v0.4.0", "0.3.9", true},
		{"v1.0.0", "0.99.99", true},
		{"v0.10.0", "0.9.0", true},
		{"v0.9.0", "0.10.0", false},
		{"v0.3.1", "v0.3.0", true},
		{"v1.0.0", "2.0.0", false},
		{"v0.3.0-rc1", "0.2.0", false},
		{"v0.3.0+build", "0.2.0", false},
		{"0.4.0", "0.3.0", false}, // a tag without v is not a release tag
		{"v0.4", "0.3.0", false},
		{"v0.4.0.1", "0.3.0", false},
		{"v0.+4.0", "0.3.0", false},
		{"v99999999999999999999.0.0", "0.3.0", false},
		{"latest", "0.3.0", false},
		{"", "0.3.0", false},
		{"v0.4.0", "0.0.0-dev", false},
		{"v0.4.0", "", false},
		{"v0.4.0", "junk", false},
	} {
		if got := Newer(c.tag, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.tag, c.current, got)
		}
	}
}

func TestCacheRoundTripIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if c, err := LoadCache(dir); err != nil || c != (Cache{}) {
		t.Fatalf("missing cache: %+v %v", c, err)
	}
	want := Cache{Tag: "v0.4.0", CheckedAt: time.Date(2026, 11, 25, 1, 2, 3, 0, time.UTC)}
	if err := SaveCache(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCache(dir)
	if err != nil || got.Tag != want.Tag || !got.CheckedAt.Equal(want.CheckedAt) {
		t.Fatalf("%+v %v", got, err)
	}
	fi, err := os.Stat(filepath.Join(dir, "update.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
}

func TestLoadCacheCorruptIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "update.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := LoadCache(dir); err == nil || c != (Cache{}) {
		t.Fatalf("%+v %v", c, err)
	}
}

func mark(t *testing.T, dir string, names ...string) {
	t.Helper()
	d := filepath.Join(dir, "installed-by")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(d, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRoutes(t *testing.T) {
	for _, c := range []struct {
		markers []string
		want    []Route
	}{
		{nil, nil},
		{[]string{"plugin"}, []Route{RoutePlugin}},
		{[]string{"install.sh"}, []Route{RouteInstallSh}},
		{[]string{"install.sh", "plugin", "other"}, []Route{RoutePlugin, RouteInstallSh}},
	} {
		dir := t.TempDir()
		mark(t, dir, c.markers...)
		if got := Routes(dir); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: got %v", c.markers, got)
		}
	}
	if got := Routes(filepath.Join(t.TempDir(), "absent")); got != nil {
		t.Errorf("absent state dir: %v", got)
	}
}

func setRepo(t *testing.T, repo string) {
	t.Helper()
	old := version.Repo
	version.Repo = repo
	t.Cleanup(func() { version.Repo = old })
}

func TestNoticeForEachRoute(t *testing.T) {
	setRepo(t, testRepo)
	const market = "als0x-sapulidi"
	const plugin = "/plugin update sapulidi@als0x-sapulidi"
	const other = "update the plugin in your agent tool"
	both := []Route{RoutePlugin, RouteInstallSh}
	for _, c := range []struct {
		name        string
		routes      []Route
		marketplace string
		command     string
	}{
		{"plugin", []Route{RoutePlugin}, market, plugin},
		{"plugin on another host", []Route{RoutePlugin}, "", other},
		{"install.sh", []Route{RouteInstallSh}, market, "sapulidi update"},
		{"both", both, market, plugin + " or sapulidi update"},
		{"both on another host", both, "", other + " or sapulidi update"},
		{"no marker", nil, market, "see https://github.com/als0x/sapulidi/releases"},
	} {
		// The usual lengths, and both versions at 8 characters.
		for _, v := range [][2]string{{"v0.4.0", "0.3.0"}, {"v10.20.9", "10.20.08"}, {"v99.99.9", "99.99.08"}} {
			tag, current := v[0], v[1]
			got := Notice(current, Cache{Tag: tag}, c.routes, c.marketplace)
			if !strings.HasPrefix(got, "Sapu Lidi "+tag) || !strings.HasSuffix(got, c.command) {
				t.Errorf("%s %s: %q", c.name, tag, got)
			}
			if len(got) >= 80 || strings.Contains(got, "\n") {
				t.Errorf("%s %s: %d columns: %q", c.name, tag, len(got), got)
			}
		}
	}
}

func TestNoticeFullWordingWhenItFits(t *testing.T) {
	setRepo(t, testRepo)
	got := Notice("0.3.0", Cache{Tag: "v0.4.0"}, []Route{RouteInstallSh}, "")
	want := "Sapu Lidi v0.4.0 is out (you have 0.3.0). Update: sapulidi update"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestNoticeNeverOverflows(t *testing.T) {
	longTag := "v" + strings.Repeat("0", 90) + "9.0.0"
	for name, c := range map[string]struct {
		repo, tag, want string
		routes          []Route
	}{
		"long repository": {strings.Repeat("o", 60) + "/sapulidi", "v0.4.0",
			"Sapu Lidi v0.4.0 is out: run sapulidi version", nil},
		"long tag": {testRepo, longTag,
			"Sapu Lidi " + longTag[:40] + " is out: run sapulidi version", []Route{RoutePlugin, RouteInstallSh}},
		"tag that fits only the short form": {testRepo, "v0000000000000000000000009.0.0",
			"Sapu Lidi v0000000000000000000000009.0.0 is out: run sapulidi version",
			[]Route{RoutePlugin, RouteInstallSh}},
	} {
		t.Run(name, func(t *testing.T) {
			setRepo(t, c.repo)
			got := Notice("0.3.0", Cache{Tag: c.tag}, c.routes, "als0x-sapulidi")
			if got != c.want || len(got) >= 80 {
				t.Fatalf("%d columns: %q, want %q", len(got), got, c.want)
			}
		})
	}
}

// race runs Claim from many goroutines at once and counts the winners.
func race(t *testing.T, dir string) int {
	t.Helper()
	var wins atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			won, err := Claim(dir)
			if err != nil {
				t.Error(err)
			}
			if won {
				wins.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	return int(wins.Load())
}

func age(t *testing.T, path string, d time.Duration) {
	t.Helper()
	at := time.Now().Add(-d)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestClaimHasOneWinnerADay(t *testing.T) {
	for round := range 50 {
		dir := t.TempDir()
		claim := filepath.Join(dir, "update.claim")
		if n := race(t, dir); n != 1 {
			t.Fatalf("round %d, no claim yet: %d winners", round, n)
		}
		age(t, claim, 23*time.Hour)
		if n := race(t, dir); n != 0 {
			t.Fatalf("round %d, claim of today: %d winners", round, n)
		}
		for _, old := range []time.Duration{25 * time.Hour, -48 * time.Hour} { // yesterday's, or a clock that was wrong
			age(t, claim, old)
			if n := race(t, dir); n != 1 {
				t.Fatalf("round %d, claim aged %v: %d winners", round, old, n)
			}
		}
		fi, err := os.Stat(claim)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("claim %v %v", fi, err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Fatalf("round %d: left behind %v", round, entries)
		}
	}
}

// A process that died while replacing an old claim must not stop checks for good.
func TestClaimRecoversFromAnAbandonedReplacement(t *testing.T) {
	dir := t.TempDir()
	claim := filepath.Join(dir, "update.claim")
	for _, p := range []string{claim, claim + ".next"} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		age(t, p, 25*time.Hour)
	}
	first, err1 := Claim(dir)
	second, err2 := Claim(dir)
	third, err3 := Claim(dir)
	if first || !second || third || err1 != nil || err2 != nil || err3 != nil {
		t.Fatalf("%v %v %v / %v %v %v", first, second, third, err1, err2, err3)
	}
}

func TestClaimThatCannotBeWritten(t *testing.T) {
	if won, err := Claim(filepath.Join(t.TempDir(), "absent")); won || err == nil {
		t.Fatalf("%v %v", won, err)
	}
}

func TestNoticeEmpty(t *testing.T) {
	setRepo(t, testRepo)
	for _, tag := range []string{"", "v0.3.0", "v0.2.0", "v0.4.0-rc1", "v0.4.0\x1b[2J"} {
		if got := Notice("0.3.0", Cache{Tag: tag}, []Route{RouteInstallSh}, ""); got != "" {
			t.Errorf("%q: %q", tag, got)
		}
	}
	setRepo(t, "")
	if got := Notice("0.3.0", Cache{Tag: "v0.4.0"}, nil, ""); got != "" {
		t.Errorf("no repository: %q", got)
	}
}
