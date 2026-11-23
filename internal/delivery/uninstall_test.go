package delivery

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/schedule"
	"github.com/als0x/sapulidi/internal/testutil"
)

type fakeBackend struct {
	removed []string
	err     error
}

func (f *fakeBackend) Name() string                           { return "fake" }
func (f *fakeBackend) Install(schedule.Job) error             { return nil }
func (f *fakeBackend) Status(string) (schedule.Status, error) { return schedule.Status{}, nil }
func (f *fakeBackend) Remove(label string) error {
	f.removed = append(f.removed, label)
	return f.err
}

func paths(home string) config.Paths {
	return config.Paths{
		Config: filepath.Join(home, "cfg", "sapulidi", "config.toml"),
		State:  filepath.Join(home, "state", "sapulidi"),
		Data:   filepath.Join(home, "data", "sapulidi"),
	}
}

func seed(t *testing.T, p config.Paths) {
	t.Helper()
	mkVersion(t, p.Data, "0.1.0", day)
	for _, f := range []string{p.Config, config.AppliedPath(p.Config), filepath.Join(p.State, "log.jsonl"),
		filepath.Join(filepath.Dir(p.Config), "packs", "mine.toml")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUninstallRemovesScheduleAndBinaries(t *testing.T) {
	home := testutil.TempHome(t)
	p := paths(home)
	seed(t, p)
	be := &fakeBackend{}
	res, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: be})
	if err != nil {
		t.Fatal(err)
	}
	if len(be.removed) != 2 || be.removed[0] != schedule.LabelRun || be.removed[1] != schedule.LabelAgent {
		t.Fatalf("schedule labels removed: %v", be.removed)
	}
	if exists(filepath.Join(p.Data, "bin")) {
		t.Fatal("binaries still there")
	}
	if !exists(p.Config) || !exists(p.State) {
		t.Fatal("plain uninstall must keep config and state")
	}
	if len(res.Removed) == 0 {
		t.Fatal("nothing reported")
	}
}

func TestUninstallAllRemovesConfigAndState(t *testing.T) {
	home := testutil.TempHome(t)
	p := paths(home)
	seed(t, p)
	if _, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: &fakeBackend{}, All: true}); err != nil {
		t.Fatal(err)
	}
	if exists(p.Config) || exists(config.AppliedPath(p.Config)) || exists(p.State) {
		t.Fatal("config or state left behind")
	}
	if !exists(filepath.Join(filepath.Dir(p.Config), "packs", "mine.toml")) {
		t.Fatal("user packs are the user's own files and must stay")
	}
}

func TestUninstallStopsWhenScheduleRemoveFails(t *testing.T) {
	home := testutil.TempHome(t)
	p := paths(home)
	seed(t, p)
	_, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: &fakeBackend{err: errors.New("boom")}})
	if err == nil || !exists(filepath.Join(p.Data, "bin", "0.1.0")) {
		t.Fatalf("binaries must stay while the schedule could not be removed: %v", err)
	}
}

// SAPULIDI_STATE pointing at a folder that is named sapulidi but is somebody's checkout is not our state: it holds
// none of the files only sapulidi writes, so --all removes nothing at all.
func TestUninstallAllRefusesStateWithoutMarker(t *testing.T) {
	home := testutil.TempHome(t)
	p := paths(home)
	seed(t, p)
	clone := filepath.Join(home, "work", "sapulidi")
	for _, f := range []string{"go.mod", "README.md", filepath.Join("internal", "x.go"), filepath.Join(".git", "HEAD")} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(clone, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(clone, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p.State = clone
	be := &fakeBackend{}
	_, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: be, All: true})
	if err == nil {
		t.Fatal("uninstall --all accepted a state directory without a sapulidi file")
	}
	for _, f := range []string{"go.mod", "README.md", filepath.Join(".git", "HEAD")} {
		if !exists(filepath.Join(clone, f)) {
			t.Fatalf("%s was removed", f)
		}
	}
	if len(be.removed) != 0 || !exists(p.Config) || !exists(filepath.Join(p.Data, "bin", "0.1.0")) {
		t.Fatalf("something was removed before the refusal: schedule %v", be.removed)
	}
}

func TestUninstallAllAcceptsEachStateMarker(t *testing.T) {
	for _, marker := range []string{"consent.json", "log.jsonl", "lastrun.json"} {
		home := testutil.TempHome(t)
		p := paths(home)
		seed(t, p)
		if err := os.Remove(filepath.Join(p.State, "log.jsonl")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p.State, marker), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: &fakeBackend{}, All: true}); err != nil {
			t.Fatalf("%s: %v", marker, err)
		}
		if exists(p.State) {
			t.Fatalf("%s: state left behind", marker)
		}
	}
}

// refusesAndKeeps runs uninstall --all on p and expects a refusal that removed nothing: no scheduler call, the
// binaries, the config and the state are all still there.
func refusesAndKeeps(t *testing.T, name string, p config.Paths, home string) {
	t.Helper()
	be := &fakeBackend{}
	_, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: be, All: true})
	if err == nil {
		t.Fatalf("%s: uninstall --all went ahead", name)
	}
	if len(be.removed) != 0 {
		t.Fatalf("%s: the schedule was touched before the refusal: %v", name, be.removed)
	}
	for _, f := range []string{p.Config, filepath.Join(p.State, "log.jsonl"), filepath.Join(p.Data, "bin", "0.1.0")} {
		if _, err := os.Lstat(f); err != nil && !os.IsNotExist(err) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestUninstallAllRefusesConfigWithoutMarker(t *testing.T) {
	home := testutil.TempHome(t)
	p := paths(home)
	seed(t, p)
	other := filepath.Join(home, "work", "sapulidi")
	if err := os.MkdirAll(filepath.Join(other, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.Config = filepath.Join(other, "config.toml") // a folder named sapulidi that is somebody's project
	refusesAndKeeps(t, "config", p, home)
	if !exists(filepath.Join(other, "README.md")) || !exists(p.State) || !exists(filepath.Join(p.Data, "bin", "0.1.0")) {
		t.Fatal("something was removed")
	}
}

func TestUninstallAllAcceptsEachConfigMarker(t *testing.T) {
	for _, name := range []string{"config.toml", "applied.toml"} {
		home := testutil.TempHome(t)
		p := paths(home)
		seed(t, p)
		if err := os.Remove(p.Config); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(config.AppliedPath(p.Config)); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(p.Config), name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: &fakeBackend{}, All: true}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestUninstallAllRefusesDataWithoutBin(t *testing.T) {
	home := testutil.TempHome(t)
	p := paths(home)
	seed(t, p)
	other := filepath.Join(home, "work", "sapulidi") // data pointed at somebody's project
	if err := os.MkdirAll(filepath.Join(other, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "go.mod"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.Data = other
	refusesAndKeeps(t, "data", p, home)
	if !exists(filepath.Join(other, "go.mod")) {
		t.Fatal("go.mod was removed")
	}
}

func TestUninstallAllAcceptsDataWithLauncherLock(t *testing.T) {
	home := testutil.TempHome(t)
	p := paths(home)
	seed(t, p)
	if err := os.RemoveAll(filepath.Join(p.Data, "bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.Data, "bin", ".lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: &fakeBackend{}, All: true}); err != nil {
		t.Fatal(err)
	}
}

// A state, config or data path that is itself a symlink is refused: --all would follow it somewhere else.
func TestUninstallAllRefusesSymlinkedPaths(t *testing.T) {
	for _, which := range []string{"state", "configdir", "configfile", "data"} {
		home := testutil.TempHome(t)
		p := paths(home)
		seed(t, p)
		var real string
		switch which {
		case "state":
			real = p.State + "-real"
			if err := os.Rename(p.State, real); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, p.State); err != nil {
				t.Fatal(err)
			}
		case "configdir":
			dir := filepath.Dir(p.Config)
			real = dir + "-real"
			if err := os.Rename(dir, real); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, dir); err != nil {
				t.Fatal(err)
			}
		case "configfile":
			real = p.Config + ".real"
			if err := os.Rename(p.Config, real); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, p.Config); err != nil {
				t.Fatal(err)
			}
		case "data":
			real = p.Data + "-real"
			if err := os.Rename(p.Data, real); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, p.Data); err != nil {
				t.Fatal(err)
			}
		}
		be := &fakeBackend{}
		if _, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: be, All: true}); err == nil {
			t.Fatalf("%s: a symlinked path was accepted", which)
		}
		if len(be.removed) != 0 || !exists(real) {
			t.Fatalf("%s: removed before the refusal (%v) or the target is gone", which, be.removed)
		}
	}
}

func TestUninstallAllRefusesStateAroundHome(t *testing.T) {
	home := testutil.TempHome(t)
	p := paths(home)
	seed(t, p)
	p.State = home // a state override pointing at the home directory
	_, err := Uninstall(UninstallOptions{Paths: p, Home: home, Backend: &fakeBackend{}, All: true})
	if err == nil || !exists(p.Config) {
		t.Fatalf("must refuse before removing anything: %v", err)
	}
}
