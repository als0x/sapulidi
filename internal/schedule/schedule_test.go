package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/testutil"
)

var errFail = errors.New("fail")

func runJob() Job {
	return Job{
		Label:    LabelRun,
		Argv:     []string{"/data/sapulidi/bin/current/sapulidi", "run", "--trigger", "schedule"},
		Interval: 30 * time.Minute,
		LogPath:  "/state/sapulidi/schedule-run.log",
	}
}

func agentJob() Job {
	return Job{
		Label:    LabelAgent,
		Argv:     []string{"/data/sapulidi/bin/current/sapulidi", "agent", "run", "--trigger", "schedule"},
		Interval: 7 * 24 * time.Hour,
		LogPath:  "/state/sapulidi/schedule-agent.log",
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != got {
		t.Errorf("%s differs\n--- got ---\n%s\n--- want ---\n%s", name, got, b)
	}
}

func calls(r *testutil.FakeRunner) []string {
	var out []string
	for _, c := range r.Calls {
		out = append(out, strings.Join(c.Argv, " "))
	}
	return out
}

func TestPickDarwinLaunchd(t *testing.T) {
	testutil.TempHome(t)
	b := Pick(&testutil.FakePlatform{OSName: "darwin"}, &testutil.FakeRunner{})
	if b.Name() != "launchd" {
		t.Fatalf("got %s", b.Name())
	}
}

func TestPickLinuxSystemdWhenAvailable(t *testing.T) {
	testutil.TempHome(t)
	r := &testutil.FakeRunner{Paths: map[string]string{"systemctl": "/bin/systemctl"}}
	r.On([]string{"systemctl", "--user", "show-environment"}, "HOME=/x\n", nil)
	b := Pick(&testutil.FakePlatform{OSName: "linux"}, r)
	if b.Name() != "systemd" {
		t.Fatalf("got %s", b.Name())
	}
}

func TestPickLinuxCronFallback(t *testing.T) {
	testutil.TempHome(t)
	r := &testutil.FakeRunner{Paths: map[string]string{"systemctl": "/bin/systemctl", "crontab": "/usr/bin/crontab"}}
	r.On([]string{"systemctl", "--user", "show-environment"}, "", &execx.ExitError{Code: 1, Stderr: "Failed to connect to bus"})
	r.On([]string{"crontab", "-l"}, "", &execx.ExitError{Code: 1, Stderr: "no crontab for u"})
	b := Pick(&testutil.FakePlatform{OSName: "linux"}, r)
	if b.Name() != "cron" {
		t.Fatalf("got %s", b.Name())
	}
	st, err := b.Status(LabelRun)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(st.Warnings, "|")
	if !strings.Contains(joined, "systemd user manager not running") || !strings.Contains(joined, "loginctl enable-linger") {
		t.Errorf("warnings missing the why: %q", joined)
	}
	// No systemctl at all also falls back.
	b2 := Pick(&testutil.FakePlatform{OSName: "linux"}, &testutil.FakeRunner{})
	if b2.Name() != "cron" {
		t.Fatalf("got %s", b2.Name())
	}
}

func newLaunchd(t *testing.T, r *testutil.FakeRunner) (*Launchd, string) {
	home := testutil.TempHome(t)
	return &Launchd{Home: home, UID: 501, PathEnv: "/usr/bin:/opt/tools/bin", Run: r, Stat: binExists}, home
}

func TestLaunchdPlistContent(t *testing.T) {
	r := &testutil.FakeRunner{}
	r.On([]string{"launchctl"}, "", nil)
	b, home := newLaunchd(t, r)
	if err := b.Install(runJob()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", LabelRun+".plist"))
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "launchd-run.plist", string(got))
	if err := b.Install(agentJob()); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", LabelAgent+".plist"))
	golden(t, "launchd-agent.plist", string(got))
}

func TestLaunchdInstallCallsBootoutThenBootstrap(t *testing.T) {
	r := &testutil.FakeRunner{}
	// bootout of a label that is not loaded fails; Install must ignore that.
	r.On([]string{"launchctl", "bootout"}, "", &execx.ExitError{Code: 113, Stderr: "not found"})
	r.On([]string{"launchctl", "bootstrap"}, "", nil)
	b, home := newLaunchd(t, r)
	if err := b.Install(runJob()); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", LabelRun+".plist")
	want := []string{
		"launchctl bootout gui/501/" + LabelRun,
		"launchctl bootstrap gui/501 " + plist,
	}
	got := calls(r)
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("calls %v want %v", got, want)
	}
	// A failing bootstrap is an error.
	r2 := &testutil.FakeRunner{}
	r2.On([]string{"launchctl", "bootout"}, "", nil)
	r2.On([]string{"launchctl", "bootstrap"}, "", errFail)
	b2, _ := newLaunchd(t, r2)
	if err := b2.Install(runJob()); err == nil {
		t.Fatal("bootstrap failure not reported")
	}
}

func TestLaunchdRemove(t *testing.T) {
	r := &testutil.FakeRunner{}
	r.On([]string{"launchctl"}, "", nil)
	b, home := newLaunchd(t, r)
	if err := b.Install(runJob()); err != nil {
		t.Fatal(err)
	}
	r.Calls = nil
	if err := b.Remove(LabelRun); err != nil {
		t.Fatal(err)
	}
	if got := calls(r); len(got) != 1 || got[0] != "launchctl bootout gui/501/"+LabelRun {
		t.Fatalf("calls %v", got)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", LabelRun+".plist")); !os.IsNotExist(err) {
		t.Fatal("plist still there")
	}
	// Removing what is not installed is not an error.
	if err := b.Remove(LabelRun); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchdStatusUsesExitCode(t *testing.T) {
	r := &testutil.FakeRunner{}
	r.On([]string{"launchctl", "print"}, "Bad request.\n", &execx.ExitError{Code: 113, Stderr: "Could not find service"})
	b, _ := newLaunchd(t, r)
	st, err := b.Status(LabelRun)
	if err != nil || st.Installed {
		t.Fatalf("st=%+v err=%v", st, err)
	}
	// Exit 0 means installed even when the text says nothing useful.
	r2 := &testutil.FakeRunner{}
	r2.On([]string{"launchctl", "print"}, "gui/501/x = {\n\truns = 3\n\tlast exit code = 0\n\trun interval = 1800 seconds\n}\n", nil)
	b2, _ := newLaunchd(t, r2)
	st, err = b2.Status(LabelRun)
	if err != nil || !st.Installed || !strings.Contains(st.Detail, "runs = 3") {
		t.Fatalf("st=%+v err=%v", st, err)
	}
}

func newSystemd(t *testing.T, r *testutil.FakeRunner) (*Systemd, string) {
	home := testutil.TempHome(t)
	return &Systemd{Home: home, User: "tester", PathEnv: "/usr/bin:/opt/tools/bin", Run: r}, home
}

func TestSystemdUnitsContent(t *testing.T) {
	r := &testutil.FakeRunner{}
	r.On([]string{"systemctl"}, "", nil)
	b, home := newSystemd(t, r)
	if err := b.Install(runJob()); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	svc, err := os.ReadFile(filepath.Join(dir, LabelRun+".service"))
	if err != nil {
		t.Fatal(err)
	}
	tmr, err := os.ReadFile(filepath.Join(dir, LabelRun+".timer"))
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "systemd-run.service", string(svc))
	golden(t, "systemd-run.timer", string(tmr))
}

func TestSystemdInstallCommands(t *testing.T) {
	r := &testutil.FakeRunner{}
	r.On([]string{"systemctl"}, "", nil)
	b, _ := newSystemd(t, r)
	if err := b.Install(runJob()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable --now " + LabelRun + ".timer",
		"systemctl --user restart " + LabelRun + ".timer",
	}
	got := calls(r)
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("calls %v want %v", got, want)
	}
	for _, c := range got {
		if strings.Contains(c, "enable-linger") {
			t.Fatal("must never enable linger")
		}
	}
}

func TestSystemdRemove(t *testing.T) {
	r := &testutil.FakeRunner{}
	r.On([]string{"systemctl"}, "", nil)
	b, home := newSystemd(t, r)
	if err := b.Install(runJob()); err != nil {
		t.Fatal(err)
	}
	if err := b.Remove(LabelRun); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	for _, ext := range []string{".service", ".timer"} {
		if _, err := os.Stat(filepath.Join(dir, LabelRun+ext)); !os.IsNotExist(err) {
			t.Fatalf("%s still there", ext)
		}
	}
}

func TestSystemdLingerWarning(t *testing.T) {
	r := &testutil.FakeRunner{}
	r.On([]string{"systemctl", "--user", "is-enabled"}, "enabled\n", nil)
	r.On([]string{"systemctl", "--user", "show"}, "", nil)
	r.On([]string{"loginctl", "show-user", "tester", "-p", "Linger", "--value"}, "no\n", nil)
	b, _ := newSystemd(t, r)
	st, err := b.Status(LabelRun)
	if err != nil || !st.Installed {
		t.Fatalf("st=%+v err=%v", st, err)
	}
	if !strings.Contains(strings.Join(st.Warnings, "|"), "loginctl enable-linger tester") {
		t.Errorf("no linger warning: %v", st.Warnings)
	}
	// Linger on: no warning.
	r2 := &testutil.FakeRunner{}
	r2.On([]string{"systemctl", "--user"}, "enabled\n", nil)
	r2.On([]string{"loginctl", "show-user"}, "yes\n", nil)
	b2, _ := newSystemd(t, r2)
	st, _ = b2.Status(LabelRun)
	if len(st.Warnings) != 0 {
		t.Errorf("unexpected warnings %v", st.Warnings)
	}
	for _, c := range append(calls(r), calls(r2)...) {
		if strings.Contains(c, "enable-linger") {
			t.Fatal("must never enable linger")
		}
	}
}

// fakeCrontab is a tiny stateful crontab behind a FakeRunner-compatible Runner.
type fakeCrontab struct {
	content string
	has     bool
	calls   []string
}

func (f *fakeCrontab) Run(_ context.Context, _ string, argv ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(argv, " "))
	switch {
	case len(argv) == 2 && argv[1] == "-l":
		if !f.has {
			return nil, &execx.ExitError{Code: 1, Stderr: "no crontab for tester"}
		}
		return []byte(f.content), nil
	case len(argv) == 2:
		b, err := os.ReadFile(argv[1])
		if err != nil {
			return nil, err
		}
		f.content, f.has = string(b), true
		return nil, nil
	}
	return nil, errFail
}

func (f *fakeCrontab) LookPath(name string) (string, error) { return "/usr/bin/" + name, nil }

func TestCronInstallKeepsOtherLines(t *testing.T) {
	home := testutil.TempHome(t)
	other := "# mine\nMAILTO=me@example.com\n0 1 * * * /usr/bin/backup --now\n"
	f := &fakeCrontab{content: other, has: true}
	b := &Cron{Home: home, PathEnv: "/usr/bin", Run: f}
	if err := b.Install(runJob()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.content, other) {
		t.Fatalf("other lines changed:\n%s", f.content)
	}
	if !strings.Contains(f.content, "# sapulidi:"+LabelRun+" begin\n") || !strings.Contains(f.content, "# sapulidi:"+LabelRun+" end\n") {
		t.Fatalf("markers missing:\n%s", f.content)
	}
	if !strings.Contains(f.content, "*/30 * * * * ") {
		t.Fatalf("schedule missing:\n%s", f.content)
	}
	if err := b.Install(agentJob()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.content, "17 3 * * 0 ") {
		t.Fatalf("agent schedule missing:\n%s", f.content)
	}
}

func TestCronReplaceBlock(t *testing.T) {
	home := testutil.TempHome(t)
	other := "0 1 * * * /usr/bin/backup\n"
	f := &fakeCrontab{content: other, has: true}
	b := &Cron{Home: home, PathEnv: "/usr/bin", Run: f}
	if err := b.Install(runJob()); err != nil {
		t.Fatal(err)
	}
	if err := b.Install(agentJob()); err != nil {
		t.Fatal(err)
	}
	first := f.content
	if err := b.Install(runJob()); err != nil {
		t.Fatal(err)
	}
	if f.content != first {
		t.Fatalf("reinstall changed the crontab:\n%s\n---\n%s", f.content, first)
	}
	if n := strings.Count(f.content, "# sapulidi:"+LabelRun+" begin"); n != 1 {
		t.Fatalf("%d run blocks", n)
	}
	if !strings.Contains(f.content, "# sapulidi:"+LabelAgent+" begin") {
		t.Fatal("agent block lost")
	}
}

func TestCronRemoveOnlyOwnBlock(t *testing.T) {
	home := testutil.TempHome(t)
	other := "# mine\n0 1 * * * /usr/bin/backup\n"
	f := &fakeCrontab{content: other, has: true}
	b := &Cron{Home: home, PathEnv: "/usr/bin", Run: f}
	_ = b.Install(runJob())
	_ = b.Install(agentJob())
	if err := b.Remove(LabelRun); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.content, LabelRun) || !strings.Contains(f.content, LabelAgent) || !strings.HasPrefix(f.content, other) {
		t.Fatalf("wrong removal:\n%s", f.content)
	}
	if err := b.Remove(LabelAgent); err != nil {
		t.Fatal(err)
	}
	if f.content != other {
		t.Fatalf("not byte for byte:\n%q\n%q", f.content, other)
	}
	// Removing with no crontab at all does nothing.
	f2 := &fakeCrontab{}
	b2 := &Cron{Home: home, PathEnv: "/usr/bin", Run: f2}
	if err := b2.Remove(LabelRun); err != nil || len(f2.calls) != 1 {
		t.Fatalf("err=%v calls=%v", err, f2.calls)
	}
}

func TestCronListFailureIsNotEmpty(t *testing.T) {
	home := testutil.TempHome(t)
	r := &testutil.FakeRunner{}
	r.On([]string{"crontab", "-l"}, "", &execx.ExitError{Code: 1, Stderr: "permission denied"})
	b := &Cron{Home: home, PathEnv: "/usr/bin", Run: r}
	if err := b.Install(runJob()); err == nil {
		t.Fatal("a failed crontab -l must not be treated as an empty crontab")
	}
	if len(r.Calls) != 1 {
		t.Fatalf("wrote after a failed read: %v", calls(r))
	}
}

func unusualPaths(t *testing.T) config.Paths {
	root := t.TempDir()
	return config.Paths{
		Config: filepath.Join(root, "my cfg"),
		State:  filepath.Join(root, "state dir's"),
		Data:   filepath.Join(root, "data"),
	}
}

func TestJobsCarryResolvedPaths(t *testing.T) {
	p := unusualPaths(t)
	want := []EnvVar{{"SAPULIDI_CONFIG", p.Config}, {"SAPULIDI_STATE", p.State}, {"SAPULIDI_DATA", p.Data}}
	for _, j := range Jobs(p) {
		if len(j.Env) != 3 {
			t.Fatalf("%s env %v", j.Label, j.Env)
		}
		for i, w := range want {
			if j.Env[i] != w {
				t.Errorf("%s env[%d] = %v, want %v", j.Label, i, j.Env[i], w)
			}
		}
	}
	j := Jobs(p)[0]

	r := &testutil.FakeRunner{}
	r.On([]string{"launchctl"}, "", nil)
	l, home := newLaunchd(t, r)
	if err := l.Install(j); err != nil {
		t.Fatal(err)
	}
	plist, _ := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", LabelRun+".plist"))
	for _, w := range []string{"<key>SAPULIDI_STATE</key>\n\t\t<string>" + xmlText(p.State) + "</string>",
		"<key>SAPULIDI_CONFIG</key>", "<key>SAPULIDI_DATA</key>", "<key>PATH</key>"} {
		if !strings.Contains(string(plist), w) {
			t.Errorf("plist lacks %q:\n%s", w, plist)
		}
	}

	r2 := &testutil.FakeRunner{}
	r2.On([]string{"systemctl"}, "", nil)
	sd := &Systemd{Home: t.TempDir(), User: "u", PathEnv: "/usr/bin", Run: r2}
	unit := sd.service(j)
	for _, w := range []string{"Environment=" + envQuote("SAPULIDI_STATE="+p.State) + "\n",
		"Environment=" + envQuote("SAPULIDI_CONFIG="+p.Config) + "\n",
		"Environment=" + envQuote("SAPULIDI_DATA="+p.Data) + "\n"} {
		if !strings.Contains(unit, w) {
			t.Errorf("unit lacks %q:\n%s", w, unit)
		}
	}

	c := &Cron{Home: t.TempDir(), PathEnv: "/usr/bin"}
	blk, err := c.block(j, "/usr/bin")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{" SAPULIDI_STATE=" + quoteSh(p.State) + " ", " SAPULIDI_CONFIG=" + quoteSh(p.Config) + " ",
		" SAPULIDI_DATA=" + quoteSh(p.Data) + " "} {
		if !strings.Contains(blk, w) {
			t.Errorf("cron line lacks %q:\n%s", w, blk)
		}
	}
}

func TestEnvControlCharactersRefused(t *testing.T) {
	j := runJob()
	j.Env = []EnvVar{{"SAPULIDI_STATE", "/tmp/x\n* * * * * rm -rf ~"}}
	r := &testutil.FakeRunner{}
	b, home := newLaunchd(t, r)
	if err := b.Install(j); err == nil {
		t.Fatal("launchd accepted a control character in an env value")
	}
	if _, err := os.Stat(filepath.Join(home, "Library")); !os.IsNotExist(err) || len(r.Calls) != 0 {
		t.Fatal("launchd wrote or ran after the refusal")
	}
	c := &Cron{Home: t.TempDir(), PathEnv: "/usr/bin", Run: &testutil.FakeRunner{}}
	if err := c.Install(j); err == nil {
		t.Fatal("cron accepted a control character in an env value")
	}
	j.Env = []EnvVar{{"SAPULIDI_STATE", "/tmp/50%"}}
	if err := c.Install(j); err == nil {
		t.Fatal("cron accepted % in an env value")
	}
}

func TestEnvDriftNamesChangedPaths(t *testing.T) {
	p := unusualPaths(t)
	j := Jobs(p)[0]
	r := &testutil.FakeRunner{}
	r.On([]string{"launchctl"}, "", nil)
	l, home := newLaunchd(t, r)
	if err := l.Install(j); err != nil {
		t.Fatal(err)
	}
	if w := EnvDrift(l, j); len(w) != 0 {
		t.Fatalf("fresh install drifts: %v", w)
	}
	// same job, the shell now resolves another state dir
	moved := p
	moved.State = filepath.Join(filepath.Dir(p.State), "elsewhere")
	w := EnvDrift(l, Jobs(moved)[0])
	if len(w) != 1 || !strings.Contains(w[0], "SAPULIDI_STATE") || !strings.Contains(w[0], "schedule install") {
		t.Fatalf("drift warning %v", w)
	}
	// a job installed before the fix has no SAPULIDI_ variables at all
	old := runJob()
	if err := l.Install(old); err != nil {
		t.Fatal(err)
	}
	if w := EnvDrift(l, j); len(w) != 3 {
		t.Fatalf("old job: %v", w)
	}
	// not installed: nothing to compare
	if err := os.Remove(filepath.Join(home, "Library", "LaunchAgents", LabelRun+".plist")); err != nil {
		t.Fatal(err)
	}
	if w := EnvDrift(l, j); len(w) != 0 {
		t.Fatalf("missing plist drifts: %v", w)
	}
	// systemd
	sd := &Systemd{Home: t.TempDir(), User: "u", PathEnv: "/usr/bin", Run: &testutil.FakeRunner{}}
	if err := os.MkdirAll(sd.dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd.dir(), j.Label+".service"), []byte(sd.service(j)), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := EnvDrift(sd, j); len(w) != 0 {
		t.Fatalf("systemd fresh drifts: %v", w)
	}
	if w := EnvDrift(sd, Jobs(moved)[0]); len(w) != 1 {
		t.Fatalf("systemd drift %v", w)
	}
	// cron
	blk, _ := (&Cron{PathEnv: "/usr/bin"}).block(j, "/usr/bin")
	cr := &Cron{Home: t.TempDir(), PathEnv: "/usr/bin", Run: &testutil.FakeRunner{}}
	cr.Run.(*testutil.FakeRunner).On([]string{"crontab", "-l"}, "# mine\n"+blk, nil)
	if w := EnvDrift(cr, j); len(w) != 0 {
		t.Fatalf("cron fresh drifts: %v", w)
	}
	if w := EnvDrift(cr, Jobs(moved)[0]); len(w) != 1 {
		t.Fatalf("cron drift %v", w)
	}
}

func TestArgvUsesCurrentNotPluginCache(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	p := config.Paths{Data: data, State: filepath.Join(data, "state")}
	jobs := Jobs(p)
	want := filepath.Join(data, "bin", "current", "sapulidi")
	if len(jobs) != 2 || jobs[0].Label != LabelRun || jobs[1].Label != LabelAgent {
		t.Fatalf("jobs %+v", jobs)
	}
	for _, j := range jobs {
		if j.Argv[0] != want || strings.Contains(strings.Join(j.Argv, " "), "plugins") {
			t.Errorf("argv %v", j.Argv)
		}
		if !filepath.IsAbs(j.LogPath) {
			t.Errorf("log %q", j.LogPath)
		}
	}
	if got := strings.Join(jobs[0].Argv[1:], " "); got != "run --trigger schedule" {
		t.Errorf("run argv %q", got)
	}
	if got := strings.Join(jobs[1].Argv[1:], " "); got != "agent run --trigger schedule" {
		t.Errorf("agent argv %q", got)
	}
	if jobs[0].Interval != 30*time.Minute || jobs[1].Interval != 7*24*time.Hour {
		t.Errorf("intervals %v %v", jobs[0].Interval, jobs[1].Interval)
	}
}
