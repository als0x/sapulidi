package schedule

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/testutil"
)

func binExists(string) (os.FileInfo, error) { return nil, nil }

// badValues are job values with a control character in a different field each.
func badJobs(pathEnv string) []struct {
	name string
	job  Job
	path string
} {
	var out []struct {
		name string
		job  Job
		path string
	}
	for _, c := range []string{"\n", "\r", "\x00"} {
		j := runJob()
		j.Argv = []string{"/tmp/x" + c + "* * * * * rm -rf ~/sapulidi", "run"}
		out = append(out, struct {
			name string
			job  Job
			path string
		}{"argv" + c, j, pathEnv})
		j = runJob()
		j.LogPath = "/tmp/log" + c + "x"
		out = append(out, struct {
			name string
			job  Job
			path string
		}{"log" + c, j, pathEnv})
		j = runJob()
		j.Label = "dev.sapulidi.run" + c
		out = append(out, struct {
			name string
			job  Job
			path string
		}{"label" + c, j, pathEnv})
		out = append(out, struct {
			name string
			job  Job
			path string
		}{"path" + c, runJob(), "/usr/bin" + c + "x"})
	}
	return out
}

func TestControlCharactersRefusedLaunchd(t *testing.T) {
	for _, tc := range badJobs("/usr/bin") {
		r := &testutil.FakeRunner{}
		b, home := newLaunchd(t, r)
		b.PathEnv = tc.path
		if err := b.Install(tc.job); err == nil {
			t.Fatalf("%q: not refused", tc.name)
		}
		if len(r.Calls) != 0 {
			t.Fatalf("%q: ran %v", tc.name, calls(r))
		}
		if _, err := os.Stat(filepath.Join(home, "Library")); !os.IsNotExist(err) {
			t.Fatalf("%q: wrote files", tc.name)
		}
	}
}

func TestControlCharactersRefusedSystemd(t *testing.T) {
	for _, tc := range badJobs("/usr/bin") {
		r := &testutil.FakeRunner{}
		b, home := newSystemd(t, r)
		b.PathEnv = tc.path
		if err := b.Install(tc.job); err == nil {
			t.Fatalf("%q: not refused", tc.name)
		}
		if len(r.Calls) != 0 {
			t.Fatalf("%q: ran %v", tc.name, calls(r))
		}
		if _, err := os.Stat(filepath.Join(home, ".config", "systemd")); !os.IsNotExist(err) {
			t.Fatalf("%q: wrote files", tc.name)
		}
	}
}

func TestControlCharactersRefusedCron(t *testing.T) {
	for _, tc := range badJobs("/usr/bin") {
		home := testutil.TempHome(t)
		f := &fakeCrontab{content: "0 1 * * * /usr/bin/backup\n", has: true}
		b := &Cron{Home: home, PathEnv: tc.path, Run: f}
		if err := b.Install(tc.job); err == nil {
			t.Fatalf("%q: not refused", tc.name)
		}
		if len(f.calls) != 0 || f.content != "0 1 * * * /usr/bin/backup\n" {
			t.Fatalf("%q: touched the crontab: %v %q", tc.name, f.calls, f.content)
		}
	}
}

func TestCronNeverGainsExtraLine(t *testing.T) {
	home := testutil.TempHome(t)
	f := &fakeCrontab{}
	b := &Cron{Home: home, PathEnv: "/usr/bin", Run: f}
	data := "/tmp/x\n* * * * * rm -rf ~"
	j := runJob()
	j.Argv[0] = data + "/bin/current/sapulidi"
	if err := b.Install(j); err == nil {
		t.Fatal("not refused")
	}
	if strings.Contains(f.content, "rm -rf") {
		t.Fatalf("extra line:\n%s", f.content)
	}
}

func TestSystemdPercentAndDollar(t *testing.T) {
	r := &testutil.FakeRunner{}
	r.On([]string{"systemctl"}, "", nil)
	b, home := newSystemd(t, r)
	b.PathEnv = "/usr/bin:/opt/$HOME/bin"
	j := runJob()
	j.LogPath = "/state/%h/log"
	if err := b.Install(j); err != nil {
		t.Fatal(err)
	}
	svc, err := os.ReadFile(filepath.Join(home, ".config", "systemd", "user", LabelRun+".service"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(svc)
	if !strings.Contains(s, "StandardError=append:/state/%%h/log\n") {
		t.Errorf("log path percent not doubled:\n%s", s)
	}
	if !strings.Contains(s, `Environment="PATH=/usr/bin:/opt/$HOME/bin"`+"\n") {
		t.Errorf("$ in Environment must stay single:\n%s", s)
	}
}

func TestCronMarkersOnlyAtLineStart(t *testing.T) {
	home := testutil.TempHome(t)
	other := "0 1 * * * echo '# sapulidi:" + LabelRun + " begin'\n"
	f := &fakeCrontab{content: other, has: true}
	b := &Cron{Home: home, PathEnv: "/usr/bin", Run: f}
	st, err := b.Status(LabelRun)
	if err != nil || st.Installed {
		t.Fatalf("mid-line marker counted: %+v %v", st, err)
	}
	if err := b.Install(runJob()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.content, other) || strings.Count(f.content, "# sapulidi:"+LabelRun+" begin\n") != 1 {
		t.Fatalf("user line altered or block missing:\n%s", f.content)
	}
	if err := b.Remove(LabelRun); err != nil {
		t.Fatal(err)
	}
	if f.content != other {
		t.Fatalf("remove touched the user line:\n%q", f.content)
	}
}

func TestCronBeginWithoutEndIsAnError(t *testing.T) {
	home := testutil.TempHome(t)
	broken := "0 1 * * * /usr/bin/backup\n# sapulidi:" + LabelRun + " begin\n*/30 * * * * x\n"
	f := &fakeCrontab{content: broken, has: true}
	b := &Cron{Home: home, PathEnv: "/usr/bin", Run: f}
	if err := b.Install(runJob()); err == nil {
		t.Fatal("Install must fail")
	}
	if err := b.Remove(LabelRun); err == nil {
		t.Fatal("Remove must fail")
	}
	if _, err := b.Status(LabelRun); err == nil {
		t.Fatal("Status must fail")
	}
	if f.content != broken {
		t.Fatalf("crontab changed:\n%q", f.content)
	}
	for _, c := range f.calls {
		if c != "crontab -l" {
			t.Fatalf("wrote: %v", f.calls)
		}
	}
}

func TestCronRejectsPercent(t *testing.T) {
	home := testutil.TempHome(t)
	f := &fakeCrontab{}
	b := &Cron{Home: home, PathEnv: "/usr/bin", Run: f}
	j := runJob()
	j.LogPath = "/state/100%/log"
	if err := b.Install(j); err == nil {
		t.Fatal("log path with % accepted")
	}
	j = runJob()
	j.Argv = append(j.Argv, "50%")
	if err := b.Install(j); err == nil {
		t.Fatal("argv with % accepted")
	}
	b.PathEnv = "/usr/bin:/a%b"
	if err := b.Install(runJob()); err == nil {
		t.Fatal("PATH with % accepted")
	}
	if len(f.calls) != 0 {
		t.Fatalf("ran %v", f.calls)
	}
}

func TestLaunchdRefusesMissingBinary(t *testing.T) {
	r := &testutil.FakeRunner{}
	b, home := newLaunchd(t, r)
	b.Stat = nil // the real os.Stat: /data/... does not exist
	if err := b.Install(runJob()); err == nil {
		t.Fatal("missing binary accepted")
	}
	if len(r.Calls) != 0 {
		t.Fatalf("ran %v", calls(r))
	}
	if _, err := os.Stat(filepath.Join(home, "Library")); !os.IsNotExist(err) {
		t.Fatal("wrote a plist")
	}
}

func TestLaunchdBootstrapFailureRemovesPlist(t *testing.T) {
	r := &testutil.FakeRunner{}
	r.On([]string{"launchctl", "bootout"}, "", nil)
	r.On([]string{"launchctl", "bootstrap"}, "", &execx.ExitError{Code: 5, Stderr: "Input/output error"})
	b, home := newLaunchd(t, r)
	if err := b.Install(runJob()); err == nil {
		t.Fatal("not reported")
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", LabelRun+".plist")); !os.IsNotExist(err) {
		t.Fatal("plist left behind")
	}
}

func TestEmptyPathFallsBack(t *testing.T) {
	r := &testutil.FakeRunner{}
	r.On([]string{"launchctl"}, "", nil)
	r.On([]string{"systemctl"}, "", nil)
	l, lhome := newLaunchd(t, r)
	l.PathEnv = ""
	if err := l.Install(runJob()); err != nil {
		t.Fatal(err)
	}
	p, _ := os.ReadFile(filepath.Join(lhome, "Library", "LaunchAgents", LabelRun+".plist"))
	if !strings.Contains(string(p), "<string>/usr/bin:/bin</string>") {
		t.Errorf("launchd:\n%s", p)
	}
	s, shome := newSystemd(t, r)
	s.PathEnv = ""
	if err := s.Install(runJob()); err != nil {
		t.Fatal(err)
	}
	u, _ := os.ReadFile(filepath.Join(shome, ".config", "systemd", "user", LabelRun+".service"))
	if !strings.Contains(string(u), `Environment="PATH=/usr/bin:/bin"`) {
		t.Errorf("systemd:\n%s", u)
	}
	f := &fakeCrontab{}
	c := &Cron{Home: shome, Run: f}
	if err := c.Install(runJob()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.content, "PATH='/usr/bin:/bin' ") {
		t.Errorf("cron:\n%s", f.content)
	}
}
