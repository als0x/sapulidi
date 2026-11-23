package schedule

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/testutil"
)

func TestParseInterval(t *testing.T) {
	ok := map[string]time.Duration{
		"5m": 5 * time.Minute, "300s": 5 * time.Minute, "90m": 90 * time.Minute,
		"2h": 2 * time.Hour, "1d": 24 * time.Hour, "7d": 7 * 24 * time.Hour,
	}
	for in, want := range ok {
		if got, err := ParseInterval(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"4m", "30s", "8d", "0m", "-5m", "5", "5x", "1.5h", "330s", ""} {
		_, err := ParseInterval(in)
		if err == nil || !strings.Contains(err.Error(), "interval") {
			t.Errorf("%q: error %v", in, err)
		}
	}
	if _, err := ParseInterval("5"); err == nil || !strings.Contains(err.Error(), "write 30m, 2h or 1d") {
		t.Errorf("no unit: %v", err)
	}
}

func jobEvery(d time.Duration) Job {
	j := runJob()
	j.Interval = d
	return j
}

var intervalCases = []struct {
	every   time.Duration
	seconds int64
	cron    string // "" when cron refuses
}{
	{5 * time.Minute, 300, "*/5 * * * *"},
	{30 * time.Minute, 1800, "*/30 * * * *"},
	{90 * time.Minute, 5400, ""},
	{2 * time.Hour, 7200, "0 */2 * * *"},
	{24 * time.Hour, 86400, "17 3 * * *"},
	{2 * 24 * time.Hour, 172800, ""},
	{7 * 24 * time.Hour, 604800, "17 3 * * 0"},
}

func TestRenderedIntervalPerBackend(t *testing.T) {
	for _, c := range intervalCases {
		l := &Launchd{Home: "/h", UID: 1}
		if got := l.render(jobEvery(c.every)); !strings.Contains(got, fmt.Sprintf("<key>StartInterval</key>\n\t<integer>%d</integer>", c.seconds)) {
			t.Errorf("%v plist:\n%s", c.every, got)
		}
		s := &Systemd{Home: "/h"}
		if got := s.timer(jobEvery(c.every)); !strings.Contains(got, fmt.Sprintf("OnUnitActiveSec=%ds\n", c.seconds)) {
			t.Errorf("%v timer:\n%s", c.every, got)
		}
		spec, err := cronSpec(c.every)
		if c.cron == "" {
			if err == nil {
				t.Errorf("%v: cron accepted %q", c.every, spec)
			}
		} else if err != nil || spec != c.cron {
			t.Errorf("%v: cron %q, %v; want %q", c.every, spec, err, c.cron)
		}
	}
}

func TestCronRefusesWhatItCannotExpressBeforeWriting(t *testing.T) {
	cases := map[time.Duration]string{
		90 * time.Minute:   "cron cannot run every 90m; use 5 to 59 minutes, whole hours up to 23, 1d or 7d",
		36 * time.Hour:     "cron cannot run every 36h; use 5 to 59 minutes, whole hours up to 23, 1d or 7d",
		2 * 24 * time.Hour: "cron cannot run every 2d; use 5 to 59 minutes, whole hours up to 23, 1d or 7d",
	}
	for d, want := range cases {
		f := &fakeCrontab{content: "0 1 * * * /usr/bin/backup\n", has: true}
		b := &Cron{Home: testutil.TempHome(t), PathEnv: "/usr/bin", Run: f}
		err := b.Install(jobEvery(d))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: error %v, want %q", d, err, want)
		}
		if len(f.calls) != 0 {
			t.Errorf("%v: touched the crontab before refusing: %v", d, f.calls)
		}
	}
}

func TestCronDailyAndWeeklyBlocks(t *testing.T) {
	f := &fakeCrontab{}
	b := &Cron{Home: testutil.TempHome(t), PathEnv: "/usr/bin", Run: f}
	if err := b.Install(jobEvery(24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.content, "17 3 * * * ") {
		t.Fatalf("daily:\n%s", f.content)
	}
	if err := b.Install(jobEvery(7 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.content, "17 3 * * 0 ") || strings.Contains(f.content, "17 3 * * * ") {
		t.Fatalf("weekly replaces daily:\n%s", f.content)
	}
}

// Changing the interval of an installed job reloads it on every backend.
func TestChangingIntervalReloadsPerBackend(t *testing.T) {
	// launchd: plist rewritten, bootout then bootstrap
	r := &testutil.FakeRunner{}
	r.On([]string{"launchctl"}, "", nil)
	lb, home := newLaunchd(t, r)
	_ = lb.Install(jobEvery(30 * time.Minute))
	r.Calls = nil
	if err := lb.Install(jobEvery(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", LabelRun+".plist")
	got := calls(r)
	if len(got) != 2 || got[0] != "launchctl bootout gui/501/"+LabelRun || got[1] != "launchctl bootstrap gui/501 "+plist {
		t.Fatalf("launchd %v", got)
	}
	if b, _ := os.ReadFile(plist); !strings.Contains(string(b), "<integer>7200</integer>") {
		t.Fatalf("plist not rewritten:\n%s", b)
	}

	// systemd: daemon-reload then restart of the timer
	r2 := &testutil.FakeRunner{}
	r2.On([]string{"systemctl"}, "", nil)
	sb, _ := newSystemd(t, r2)
	_ = sb.Install(jobEvery(30 * time.Minute))
	r2.Calls = nil
	if err := sb.Install(jobEvery(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	got = calls(r2)
	if len(got) != 3 || got[0] != "systemctl --user daemon-reload" || got[2] != "systemctl --user restart "+LabelRun+".timer" {
		t.Fatalf("systemd %v", got)
	}

	// cron: only the run block is rewritten
	f := &fakeCrontab{content: "0 1 * * * /usr/bin/backup\n", has: true}
	cb := &Cron{Home: testutil.TempHome(t), PathEnv: "/usr/bin", Run: f}
	_ = cb.Install(jobEvery(30 * time.Minute))
	_ = cb.Install(agentJob())
	if err := cb.Install(jobEvery(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.content, "0 */2 * * * ") || strings.Contains(f.content, "*/30 * * * * ") ||
		!strings.Contains(f.content, "17 3 * * 0 ") || !strings.HasPrefix(f.content, "0 1 * * * /usr/bin/backup\n") {
		t.Fatalf("cron:\n%s", f.content)
	}
}

// launchctl print of a StartInterval job shows no next run; launchd reports none.
func TestLaunchdStatusHasNoNextRun(t *testing.T) {
	r := &testutil.FakeRunner{}
	r.On([]string{"launchctl", "print"}, "gui/501/x = {\n\truns = 3\n\tlast exit code = 0\n\trun interval = 1800 seconds\n}\n", nil)
	b, _ := newLaunchd(t, r)
	st, err := b.Status(LabelRun)
	if err != nil || !st.Installed || st.Next != "" {
		t.Fatalf("%+v %v", st, err)
	}
}

// bootstrapRunner is a launchctl fake: the bootstrap calls numbered in failOn (1-based) fail, everything else works.
type bootstrapRunner struct {
	calls     []string
	bootstrap int
	failOn    map[int]bool
}

func (r *bootstrapRunner) Run(_ context.Context, _ string, argv ...string) ([]byte, error) {
	r.calls = append(r.calls, strings.Join(argv, " "))
	if len(argv) > 1 && argv[1] == "bootstrap" {
		r.bootstrap++
		if r.failOn[r.bootstrap] {
			return nil, errFail
		}
	}
	return nil, nil
}

func (r *bootstrapRunner) LookPath(name string) (string, error) { return "/bin/" + name, nil }

func TestLaunchdFailedBootstrapRestoresThePreviousJob(t *testing.T) {
	r := &bootstrapRunner{failOn: map[int]bool{2: true}}
	home := testutil.TempHome(t)
	b := &Launchd{Home: home, UID: 501, PathEnv: "/usr/bin", Run: r, Stat: binExists}
	if err := b.Install(jobEvery(30 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", LabelRun+".plist")
	old, _ := os.ReadFile(plist)
	r.calls = nil
	err := b.Install(jobEvery(2 * time.Hour))
	if err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("error %v", err)
	}
	if got, _ := os.ReadFile(plist); string(got) != string(old) {
		t.Fatalf("old plist not restored:\n%s", got)
	}
	want := []string{"launchctl bootout gui/501/" + LabelRun, "launchctl bootstrap gui/501 " + plist, "launchctl bootstrap gui/501 " + plist}
	if strings.Join(r.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls %v want %v", r.calls, want)
	}
}

func TestLaunchdPreviousJobThatCannotBeReloadedIsReported(t *testing.T) {
	r := &bootstrapRunner{failOn: map[int]bool{2: true, 3: true}}
	home := testutil.TempHome(t)
	b := &Launchd{Home: home, UID: 501, PathEnv: "/usr/bin", Run: r, Stat: binExists}
	if err := b.Install(jobEvery(30 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := b.Install(jobEvery(2 * time.Hour)); err == nil || !strings.Contains(err.Error(), "could not be loaded again") {
		t.Fatalf("error %v", err)
	}
}

func TestLaunchdFailedFirstBootstrapLeavesNoPlist(t *testing.T) {
	r := &bootstrapRunner{failOn: map[int]bool{1: true}}
	home := testutil.TempHome(t)
	b := &Launchd{Home: home, UID: 501, PathEnv: "/usr/bin", Run: r, Stat: binExists}
	if err := b.Install(runJob()); err == nil {
		t.Fatal("no error")
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", LabelRun+".plist")); !os.IsNotExist(err) {
		t.Fatal("a plist is left although there was none before")
	}
}

func TestSystemdStatusNextRun(t *testing.T) {
	r := &testutil.FakeRunner{}
	r.On([]string{"systemctl", "--user", "is-enabled"}, "enabled\n", nil)
	r.On([]string{"systemctl", "--user", "list-timers"},
		"Tue 2026-11-24 10:00:00 UTC 1h 2min left Tue 2026-11-24 09:00:00 UTC 28min ago "+LabelRun+".timer "+LabelRun+".service\n", nil)
	r.On([]string{"loginctl"}, "yes\n", nil)
	b, _ := newSystemd(t, r)
	st, err := b.Status(LabelRun)
	if err != nil || st.Next != "1h 2min" {
		t.Fatalf("%+v %v", st, err)
	}
	r2 := &testutil.FakeRunner{}
	r2.On([]string{"systemctl", "--user", "is-enabled"}, "enabled\n", nil)
	r2.On([]string{"systemctl", "--user", "list-timers"}, "", &execx.ExitError{Code: 1})
	r2.On([]string{"loginctl"}, "yes\n", nil)
	b2, _ := newSystemd(t, r2)
	if st, err := b2.Status(LabelRun); err != nil || st.Next != "" {
		t.Fatalf("a failing list-timers must only drop the next run: %+v %v", st, err)
	}
}

func TestCronStatusHasNoNextRun(t *testing.T) {
	f := &fakeCrontab{}
	b := &Cron{Home: testutil.TempHome(t), PathEnv: "/usr/bin", Run: f}
	_ = b.Install(runJob())
	if st, _ := b.Status(LabelRun); !st.Installed || st.Next != "" {
		t.Fatalf("%+v", st)
	}
}

func TestJobsUseStoredInterval(t *testing.T) {
	p := unusualPaths(t)
	run := func() Job {
		for _, j := range Jobs(p) {
			if j.Label == LabelRun {
				return j
			}
		}
		t.Fatal("no run job")
		return Job{}
	}
	if got := run().Interval; got != 30*time.Minute {
		t.Fatalf("default %v", got)
	}
	if err := config.Apply(p, config.Change{Key: "schedule.interval", Value: "2h"}); err != nil {
		t.Fatal(err)
	}
	if got := run().Interval; got != 2*time.Hour {
		t.Fatalf("stored %v", got)
	}
	for _, j := range Jobs(p) {
		if j.Label == LabelAgent && j.Interval != 7*24*time.Hour {
			t.Fatalf("the agent keeps its own interval: %v", j.Interval)
		}
	}
	// No config path (a test fixture, a broken setup): the default, never a file in the working directory.
	if got := Jobs(config.Paths{Data: "/d", State: "/s"})[0].Interval; got != 30*time.Minute {
		t.Fatalf("no path: %v", got)
	}
}
