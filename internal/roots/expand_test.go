package roots

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
)

func (f *fixture) mkdir(t *testing.T, rel string) string {
	t.Helper()
	d := filepath.Join(f.home, rel)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// emptyRepo is a repository without a commit: enough for discovery and much faster than MakeRepo.
func (f *fixture) emptyRepo(t *testing.T, rel string) string {
	t.Helper()
	d := f.mkdir(t, rel)
	cmd := exec.Command("git", "init", "-q", d)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return d
}

func paths(rs []core.Root) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Path)
	}
	return out
}

func keptPaths(ks []core.Kept, why string) []string {
	var out []string
	for _, k := range ks {
		if k.Why == why {
			out = append(out, k.Path)
		}
	}
	return out
}

func mentions(ks []core.Kept, path string) bool {
	return slices.ContainsFunc(ks, func(k core.Kept) bool { return k.Path == path })
}

func TestExpandFolderOfRepos(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			f := newFixture(t)
			want := []string{
				f.repo(t, "code/work/group/r4"),
				f.repo(t, "code/work/group/r5"),
				f.repo(t, "code/work/r1"),
				f.repo(t, "code/work/r2"),
				f.repo(t, "code/work/r3"),
			}
			f.emptyRepo(t, "code/work/group/deeper/depth3")
			work := filepath.Join(f.home, "code", "work")
			file := claudeFile(f, "s1")
			if agent == "codex" {
				file = filepath.Join(f.home, ".codex", "sessions", "s1.jsonl")
			}
			transcript(t, file, time.Hour, cwdLine(work))

			rs, ks := f.discover()
			if !slices.Equal(paths(rs), want) {
				t.Fatalf("roots = %v kept = %v", rs, ks)
			}
			for _, r := range rs {
				if r.Source != agent+"+children" {
					t.Fatalf("source = %q", r.Source)
				}
			}
			if mentions(ks, work) {
				t.Fatalf("expanded folder reported: %v", ks)
			}
		})
	}
}

func TestExpandNeverDescendsIntoRepository(t *testing.T) {
	f := newFixture(t)
	outer := f.repo(t, "code/work/outer")
	f.repo(t, "code/work/outer/inner")
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(filepath.Join(f.home, "code", "work")))
	rs, ks := f.discover()
	if !slices.Equal(paths(rs), []string{outer}) || len(keptPaths(ks, "nested")) != 0 {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

// A directory with a .git that git does not accept is still not a folder to search.
func TestExpandNeverDescendsIntoBrokenRepository(t *testing.T) {
	f := newFixture(t)
	f.mkdir(t, "code/work/broken/.git")
	f.emptyRepo(t, "code/work/broken/inner")
	broken := filepath.Join(f.home, "code", "work", "broken")
	work := filepath.Dir(broken)
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(work))
	rs, ks := f.discover()
	if len(rs) != 0 || !hasKept(ks, broken, "not-git") || !hasKept(ks, work, "not-git") {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

func TestExpandSkipsSymlinksHiddenOwnerAndDevice(t *testing.T) {
	f := newFixture(t)
	good := f.repo(t, "code/work/good")
	linked := f.repo(t, "code/elsewhere/linked")
	f.repo(t, "code/other/deep")
	f.repo(t, "code/work/.hidden/r")
	f.repo(t, "code/work/.dotrepo")
	f.repo(t, "code/work/group/.hidden")
	theirs := f.repo(t, "code/work/theirs")
	mnt := f.repo(t, "code/work/mnt")
	work := filepath.Join(f.home, "code", "work")
	for link, target := range map[string]string{
		filepath.Join(work, "link"):               linked,
		filepath.Join(work, "linkdir"):            filepath.Join(f.home, "code", "other"),
		filepath.Join(work, "group", "link-deep"): linked,
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	f.plat.NotOwned = map[string]bool{theirs: true}
	f.plat.OtherDevice = map[string]bool{mnt: true}
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(work))

	rs, ks := f.discover()
	if !slices.Equal(paths(rs), []string{good}) {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
	if !hasKept(ks, theirs, "owner") || !hasKept(ks, mnt, "device") {
		t.Fatalf("kept = %v", ks)
	}
}

func TestExpandRefusedFolders(t *testing.T) {
	f := newFixture(t)
	f.repo(t, "a")
	f.repo(t, "tmpbase/work/r")
	f.repo(t, "theirs/r")
	f.repo(t, "mnt/r")
	tmpBase := filepath.Join(f.home, "tmpbase")
	tmpWork := filepath.Join(tmpBase, "work")
	theirs := filepath.Join(f.home, "theirs")
	mnt := filepath.Join(f.home, "mnt")
	f.plat.NotOwned = map[string]bool{theirs: true}
	f.plat.OtherDevice = map[string]bool{mnt: true}
	for i, cwd := range []string{f.home, "/", tmpBase, tmpWork, theirs, mnt} {
		transcript(t, filepath.Join(f.home, ".claude", "projects", "p"+strconv.Itoa(i), "s.jsonl"), time.Hour, cwdLine(cwd))
	}
	rs, ks := Discover(DiscoverEnv{Home: f.home, Config: f.cfg, Platform: f.plat, Run: f.run, Now: now, TempDirs: []string{tmpBase}})
	if len(rs) != 0 {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
	for path, why := range map[string]string{f.home: "deny", "/": "deny", tmpBase: "temp", tmpWork: "temp", theirs: "owner", mnt: "device"} {
		if !hasKept(ks, path, why) {
			t.Errorf("missing %s %s in %v", path, why, ks)
		}
	}
}

func TestExpandCapsAt200(t *testing.T) {
	f := newFixture(t)
	for i := range 201 {
		f.emptyRepo(t, "code/work/r"+strconv.Itoa(1000+i))
	}
	work := filepath.Join(f.home, "code", "work")
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(work))
	rs, ks := Discover(DiscoverEnv{Home: f.home, Config: f.cfg, Platform: f.plat, Run: f.run, Now: now, Budget: 2 * time.Minute})
	if len(rs) != 200 {
		t.Fatalf("len = %d kept = %v", len(rs), ks)
	}
	if got := keptPaths(ks, "cap"); !slices.Equal(got, []string{work}) {
		t.Fatalf("cap = %v", got)
	}
}

func TestExpandOneCapForManyDroppedRepos(t *testing.T) {
	f := newFixture(t)
	for i := range 205 {
		f.emptyRepo(t, "code/work/r"+strconv.Itoa(1000+i))
	}
	work := filepath.Join(f.home, "code", "work")
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(work))
	rs, ks := Discover(DiscoverEnv{Home: f.home, Config: f.cfg, Platform: f.plat, Run: f.run, Now: now, Budget: 2 * time.Minute})
	if got := keptPaths(ks, "cap"); len(rs) != 200 || !slices.Equal(got, []string{work}) {
		t.Fatalf("len = %d cap = %v", len(rs), got)
	}
}

// Fake repositories count toward the limit, so they cannot run git until the budget ends.
func TestExpandLimitsGitRunsOnFakeRepositories(t *testing.T) {
	f := newFixture(t)
	for i := range 230 {
		f.mkdir(t, "code/work/r"+strconv.Itoa(1000+i)+"/.git")
	}
	work := filepath.Join(f.home, "code", "work")
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(work))
	rs, ks := Discover(DiscoverEnv{Home: f.home, Config: f.cfg, Platform: f.plat, Run: f.run, Now: now, Budget: 2 * time.Minute})
	if len(rs) != 0 || len(f.run.Calls) != 221 { // the folder itself and 220 entries
		t.Fatalf("roots = %v git ran %d times", rs, len(f.run.Calls))
	}
	if got := keptPaths(ks, "cap"); !slices.Equal(got, []string{work}) {
		t.Fatalf("cap = %v", got)
	}
}

func TestExpandRefusesFolderWithOwnGit(t *testing.T) {
	for name, plant := range map[string]func(dotgit string) error{
		"HEAD only":    func(p string) error { return writeFile(filepath.Join(p, "HEAD"), "garbage\n") },
		"objects only": func(p string) error { return os.MkdirAll(filepath.Join(p, "objects"), 0o755) },
		"refs only":    func(p string) error { return os.MkdirAll(filepath.Join(p, "refs"), 0o755) },
		"gitfile":      func(p string) error { return writeFile(p, "garbage\n") },
		"symlink":      func(p string) error { return os.Symlink("gone", p) },
	} {
		for _, at := range []string{"code/work/sub", "code/work", "code", "."} {
			t.Run(name+" at "+at, func(t *testing.T) {
				f := newFixture(t)
				f.emptyRepo(t, "code/work/sub/r")
				sub := filepath.Join(f.home, "code", "work", "sub")
				if err := plant(filepath.Join(f.home, at, ".git")); err != nil {
					t.Fatal(err)
				}
				transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(sub))
				rs, ks := f.discover()
				if len(rs) != 0 || !hasKept(ks, sub, "not-git") || len(f.run.Calls) != 1 {
					t.Fatalf("roots = %v kept = %v calls = %d", rs, ks, len(f.run.Calls))
				}
			})
		}
	}
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// A `.git` directory without HEAD, objects and refs, such as one holding only info/exclude, is not a
// repository: neither the folder nor an ancestor holding one blocks the expansion.
func TestExpandIgnoresDotGitThatIsNoRepository(t *testing.T) {
	for _, at := range []string{"code/work", "code", "."} {
		t.Run(at, func(t *testing.T) {
			f := newFixture(t)
			r := f.emptyRepo(t, "code/work/r")
			work := filepath.Dir(r)
			if err := writeFile(filepath.Join(f.home, at, ".git", "info", "exclude"), "r/\n"); err != nil {
				t.Fatal(err)
			}
			transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(work))
			rs, ks := f.discover()
			if !slices.Equal(paths(rs), []string{r}) || mentions(ks, work) {
				t.Fatalf("roots = %v kept = %v", rs, ks)
			}
		})
	}
}

func TestExpandSkipsSymlinkedDotGit(t *testing.T) {
	f := newFixture(t)
	real := f.emptyRepo(t, "code/elsewhere/real")
	child := f.mkdir(t, "code/work/child")
	f.emptyRepo(t, "code/work/child/inner")
	if err := os.Symlink(filepath.Join(real, ".git"), filepath.Join(child, ".git")); err != nil {
		t.Fatal(err)
	}
	work := filepath.Dir(child)
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(work))
	rs, ks := f.discover()
	if len(rs) != 0 || !hasKept(ks, work, "not-git") || len(f.run.Calls) != 1 {
		t.Fatalf("roots = %v kept = %v calls = %d", rs, ks, len(f.run.Calls))
	}
}

func TestExpandRefusesPersonalFolders(t *testing.T) {
	for _, name := range []string{"Downloads", "Desktop", "Documents", "Library", "Pictures", "Movies", "Music", "Public"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.emptyRepo(t, name+"/r")
			f.emptyRepo(t, name+"/work/r")
			top := filepath.Join(f.home, name)
			below := filepath.Join(top, "work")
			transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(top))
			transcript(t, filepath.Join(f.home, ".claude", "projects", "slug2", "s.jsonl"), time.Hour, cwdLine(below))
			rs, ks := f.discover()
			if len(rs) != 0 || !hasKept(ks, top, "deny") || !hasKept(ks, below, "deny") {
				t.Fatalf("roots = %v kept = %v", rs, ks)
			}
		})
	}
}

// Neither a symlink inside a personal folder nor the real place of a symlinked one gets around the refusal.
func TestExpandRefusesPersonalFoldersThroughSymlinks(t *testing.T) {
	f := newFixture(t)
	f.emptyRepo(t, "code/work/r")
	f.emptyRepo(t, "cloud/docs/r")
	f.mkdir(t, "Desktop")
	inside := filepath.Join(f.home, "Desktop", "link")
	target := filepath.Join(f.home, "cloud", "docs")
	for link, to := range map[string]string{inside: filepath.Join(f.home, "code", "work"), filepath.Join(f.home, "Documents"): target} {
		if err := os.Symlink(to, link); err != nil {
			t.Fatal(err)
		}
	}
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(inside))
	transcript(t, filepath.Join(f.home, ".claude", "projects", "slug2", "s.jsonl"), time.Hour, cwdLine(target))
	rs, ks := f.discover()
	if len(rs) != 0 || !hasKept(ks, inside, "deny") || !hasKept(ks, target, "deny") {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

func (f *fixture) worktree(t *testing.T, main, path string) {
	t.Helper()
	cmd := exec.Command("git", "-C", main, "worktree", "add", "-b", "wt", path)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestExpandDropsRootOutsideFolder(t *testing.T) {
	f := newFixture(t)
	main := f.repo(t, "code/main")
	work := f.mkdir(t, "code/work")
	f.worktree(t, main, filepath.Join(work, "wt"))
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(work))
	rs, ks := f.discover()
	if len(rs) != 0 || !hasKept(ks, main, "outside") {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

func TestExpandNestedInConfigRoot(t *testing.T) {
	f := newFixture(t)
	r := f.repo(t, "code/work/r")
	work := filepath.Dir(r)
	f.cfg.Roots = []string{work}
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(work))
	rs, ks := f.discover()
	if len(rs) != 1 || rs[0] != (core.Root{Path: work, Source: "config"}) || !hasKept(ks, r, "nested") {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

func TestExpandDirectRootWinsOverExpanded(t *testing.T) {
	f := newFixture(t)
	r := f.repo(t, "code/work/r")
	work := filepath.Dir(r)
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(work))
	transcript(t, filepath.Join(f.home, ".claude", "projects", "slug2", "s.jsonl"), time.Hour, cwdLine(r))
	rs, ks := f.discover()
	if len(rs) != 1 || rs[0] != (core.Root{Path: r, Source: "claude"}) || mentions(ks, work) {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

func TestExpandedOuterRepoNestsDirectInnerRepo(t *testing.T) {
	f := newFixture(t)
	outer := f.repo(t, "code/work/outer")
	inner := f.repo(t, "code/work/outer/vendor/inner")
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(inner))
	transcript(t, filepath.Join(f.home, ".claude", "projects", "slug2", "s.jsonl"), time.Hour, cwdLine(filepath.Dir(outer)))
	rs, ks := f.discover()
	if !slices.Equal(paths(rs), []string{outer}) || !hasKept(ks, inner, "nested") {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

// The 500-root cap drops expanded roots before direct ones, whatever their paths.
func TestExpandCapKeepsDirectBeforeExpanded(t *testing.T) {
	f := newFixture(t)
	for i := range 499 {
		f.cfg.Roots = append(f.cfg.Roots, f.mkdir(t, "cfg/d"+strconv.Itoa(1000+i)))
	}
	expanded := f.repo(t, "aaa/r")
	direct := f.repo(t, "zzz")
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(filepath.Dir(expanded)))
	transcript(t, filepath.Join(f.home, ".claude", "projects", "slug2", "s.jsonl"), time.Hour, cwdLine(direct))
	rs, ks := f.discover()
	if len(rs) != 500 || !slices.Contains(paths(rs), direct) || !hasKept(ks, expanded, "cap") {
		t.Fatalf("len = %d kept = %v", len(rs), ks)
	}
}

func TestExpandMapsLinkedWorktreeToMain(t *testing.T) {
	f := newFixture(t)
	// the main repository is hidden, so only its linked worktree is found
	main := f.repo(t, "code/work/.main")
	work := filepath.Dir(main)
	f.worktree(t, main, filepath.Join(work, "wt"))
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(work))
	rs, ks := f.discover()
	if len(rs) != 1 || rs[0] != (core.Root{Path: main, Source: "claude+children"}) {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

// slowRunner delays every git call made below a directory.
type slowRunner struct {
	*testutil.FakeRunner
	below string
	delay time.Duration
}

func (r slowRunner) Run(ctx context.Context, dir string, argv ...string) ([]byte, error) {
	if strings.HasPrefix(dir, r.below+string(filepath.Separator)) {
		time.Sleep(r.delay)
	}
	return r.FakeRunner.Run(ctx, dir, argv...)
}

func TestExpandStopsWhenBudgetRunsOut(t *testing.T) {
	f := newFixture(t)
	r1 := f.emptyRepo(t, "code/work/r1")
	f.emptyRepo(t, "code/work/r2")
	f.emptyRepo(t, "code/work/r3")
	work := filepath.Dir(r1)
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(work))
	run := slowRunner{FakeRunner: f.run, below: work, delay: 2100 * time.Millisecond}
	rs, ks := Discover(DiscoverEnv{Home: f.home, Config: f.cfg, Platform: f.plat, Run: run, Now: now, Budget: 2 * time.Second})
	if !slices.Equal(paths(rs), []string{r1}) || !hasKept(ks, "", "budget") {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
	if len(f.run.Calls) != 2 { // the folder itself and r1
		t.Fatalf("git ran %d times after the budget", len(f.run.Calls))
	}
}

func TestExpandBudgetIsTheOnlyReasonForACutFolder(t *testing.T) {
	f := newFixture(t)
	broken := f.mkdir(t, "code/work/a/.git")
	f.emptyRepo(t, "code/work/b")
	work := filepath.Dir(filepath.Dir(broken))
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(work))
	run := slowRunner{FakeRunner: f.run, below: work, delay: 2100 * time.Millisecond}
	rs, ks := Discover(DiscoverEnv{Home: f.home, Config: f.cfg, Platform: f.plat, Run: run, Now: now, Budget: 2 * time.Second})
	if len(rs) != 0 || !hasKept(ks, "", "budget") || mentions(ks, work) {
		t.Fatalf("roots = %v kept = %v", rs, ks)
	}
}

func TestExpandTinyBudget(t *testing.T) {
	f := newFixture(t)
	f.emptyRepo(t, "code/work/r")
	transcript(t, claudeFile(f, "s1"), time.Hour, cwdLine(filepath.Join(f.home, "code", "work")))
	rs, ks := Discover(DiscoverEnv{Home: f.home, Config: f.cfg, Platform: f.plat, Run: f.run, Now: now, Budget: time.Nanosecond})
	if len(rs) != 0 || !hasKept(ks, "", "budget") || len(f.run.Calls) != 0 {
		t.Fatalf("roots = %v kept = %v calls = %v", rs, ks, f.run.Calls)
	}
}
