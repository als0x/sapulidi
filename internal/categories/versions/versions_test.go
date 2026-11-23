package versions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/guard"
	packsload "github.com/als0x/sapulidi/internal/packs"
	"github.com/als0x/sapulidi/internal/testutil"
	"github.com/als0x/sapulidi/packs"
)

const old = 90 * 24 * time.Hour

// loadRules loads only the packs of this category, never the whole embedded set.
// The rules are loaded for the OS the fixture pretends to be, not the one the test runs on (apple is darwin-only).
func loadRules(t *testing.T, osName string) []core.Rule {
	t.Helper()
	m := fstest.MapFS{}
	for _, f := range []string{"toolchains.toml", "browsers.toml", "apple.toml"} {
		data, err := packs.FS.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		m[f] = &fstest.MapFile{Data: data}
	}
	rules, errs := packsload.LoadFor(m, "", osName)
	for _, e := range errs {
		t.Fatalf("pack error: %v", e)
	}
	return rules
}

type fixture struct {
	home string
	fr   *testutil.FakeRunner
	env  *core.Env
}

func newFixture(t *testing.T, osName string) *fixture {
	t.Helper()
	t.Setenv("SAPULIDI_TEST", "1")
	for _, v := range []string{"NVM_DIR", "FNM_DIR", "VOLTA_HOME", "PYENV_ROOT", "RUSTUP_HOME", "GOMODCACHE"} {
		t.Setenv(v, "")
	}
	home := testutil.TempHome(t)
	fr := &testutil.FakeRunner{Paths: map[string]string{}}
	env := &core.Env{
		Home: home, Now: time.Now(), Rules: loadRules(t, osName), Platform: &testutil.FakePlatform{OSName: osName},
		Run: fr, Settings: &testutil.FakeSettings{}, Log: &testutil.MemLogger{}, Ctx: context.Background(),
	}
	return &fixture{home: home, fr: fr, env: env}
}

func (f *fixture) path(parts ...string) string {
	return filepath.Join(append([]string{f.home}, parts...)...)
}

// dir creates a directory and sets its age.
func mkdir(t *testing.T, p string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Touch(t, p, age)
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) scan(t *testing.T, cat string) ([]core.Candidate, []core.Kept) {
	t.Helper()
	for _, c := range core.Categories() {
		if c.Name() == cat {
			cands, kept, err := c.Scan(f.env)
			if err != nil {
				t.Fatal(err)
			}
			return cands, kept
		}
	}
	t.Fatalf("category %s not registered", cat)
	return nil, nil
}

func names(cs []core.Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, filepath.Base(c.Path))
	}
	slices.Sort(out)
	return out
}

func TestNvmActiveFromAlias(t *testing.T) {
	f := newFixture(t, "linux")
	nvm := f.path(".nvm")
	for _, v := range []string{"v18.19.0", "v20.11.0", "v22.1.0"} {
		mkdir(t, filepath.Join(nvm, "versions", "node", v), old)
	}
	write(t, filepath.Join(nvm, "alias", "default"), "lts/iron\n")
	write(t, filepath.Join(nvm, "alias", "lts", "iron"), "v20.11.0\n")
	cands, _ := f.scan(t, "toolchains")
	if got := names(cands); !slices.Equal(got, []string{"v18.19.0"}) {
		t.Fatalf("candidates %v", got)
	}
	c := cands[0]
	if c.Removal != core.RenameDelete || c.Base != filepath.Join(nvm, "versions", "node") {
		t.Fatalf("candidate %+v", c)
	}
	if err := (&versionsCat{name: "toolchains"}).Proof(f.env, c); err != nil {
		t.Fatalf("proof: %v", err)
	}
	// a partial version in the alias resolves to the installed one
	write(t, filepath.Join(nvm, "alias", "default"), "20\n")
	if cands, _ := f.scan(t, "toolchains"); !slices.Equal(names(cands), []string{"v18.19.0"}) {
		t.Fatalf("candidates for alias 20: %v", names(cands))
	}
}

func TestNvmYoungVersionKept(t *testing.T) {
	f := newFixture(t, "linux")
	nvm := f.path(".nvm")
	mkdir(t, filepath.Join(nvm, "versions", "node", "v18.19.0"), time.Hour)
	mkdir(t, filepath.Join(nvm, "versions", "node", "v20.11.0"), old)
	mkdir(t, filepath.Join(nvm, "versions", "node", "v22.1.0"), old)
	mkdir(t, filepath.Join(nvm, "versions", "node", "v16.0.0"), old)
	write(t, filepath.Join(nvm, "alias", "default"), "v22.1.0")
	cands, kept := f.scan(t, "toolchains")
	if got := names(cands); !slices.Equal(got, []string{"v16.0.0"}) {
		t.Fatalf("candidates %v", got)
	}
	young := 0
	for _, k := range kept {
		if k.Why == "young" {
			young++
		}
	}
	if young != 1 {
		t.Fatalf("kept %v", kept)
	}
}

func TestPyenvActive(t *testing.T) {
	f := newFixture(t, "linux")
	root := f.path(".pyenv")
	for _, v := range []string{"3.9.1", "3.10.2", "3.11.4", "3.12.0"} {
		mkdir(t, filepath.Join(root, "versions", v), old)
	}
	write(t, filepath.Join(root, "version"), "3.9.1\n")
	f.fr.Paths["pyenv"] = "/fake/bin/pyenv"
	f.fr.On([]string{"pyenv", "version-name"}, "3.10.2\n", nil)
	cands, _ := f.scan(t, "toolchains")
	if got := names(cands); !slices.Equal(got, []string{"3.11.4"}) {
		t.Fatalf("candidates %v", got)
	}
}

func TestRustupUsesUninstall(t *testing.T) {
	f := newFixture(t, "linux")
	home := f.path(".rustup")
	tc := "-aarch64-apple-darwin"
	for _, v := range []string{"stable" + tc, "nightly" + tc, "1.70.0" + tc, "1.60.0" + tc} {
		mkdir(t, filepath.Join(home, "toolchains", v), old)
	}
	repo := testutil.MakeRepo(t, f.path("work", "app"))
	write(t, filepath.Join(repo, "rust-toolchain.toml"), "[toolchain]\nchannel = \"1.70.0\"\n")
	write(t, filepath.Join(repo, "rust-toolchain"), "nightly\n")
	f.env.Roots = []core.Root{{Path: repo, Source: "config"}}
	f.fr.On([]string{"rustup", "default"}, "stable"+tc+" (default)\n", nil)
	f.fr.On([]string{"rustup", "override", "list"}, "no overrides\n", nil)
	cands, _ := f.scan(t, "toolchains")
	if got := names(cands); !slices.Equal(got, []string{"1.60.0" + tc}) {
		t.Fatalf("candidates %v", got)
	}
	c := cands[0]
	want := []string{"rustup", "toolchain", "uninstall", "1.60.0" + tc}
	if c.Removal != core.ToolCommand || !slices.Equal(c.Command, want) {
		t.Fatalf("candidate %+v", c)
	}
	if !core.ToolActionKnown("rustup", "uninstall") {
		t.Fatal("rustup/uninstall is not registered")
	}
	if err := (&versionsCat{name: "toolchains"}).Proof(f.env, c); err != nil {
		t.Fatalf("proof: %v", err)
	}
	// the default changes under us: the proof must notice
	f.fr = &testutil.FakeRunner{Paths: map[string]string{}}
	f.env.Run = f.fr
	f.fr.On([]string{"rustup", "default"}, "1.60.0"+tc+" (default)\n", nil)
	f.fr.On([]string{"rustup", "override", "list"}, "no overrides\n", nil)
	if err := (&versionsCat{name: "toolchains"}).Proof(f.env, c); err == nil {
		t.Fatal("proof accepted a toolchain that became the default")
	}
}

func TestUnknownActiveKeepsAll(t *testing.T) {
	f := newFixture(t, "linux")
	// nvm: no default alias
	for _, v := range []string{"v16.0.0", "v18.0.0", "v20.0.0"} {
		mkdir(t, filepath.Join(f.path(".nvm"), "versions", "node", v), old)
	}
	// pyenv: the tool is installed but fails
	for _, v := range []string{"3.8.0", "3.9.0", "3.10.0"} {
		mkdir(t, filepath.Join(f.path(".pyenv"), "versions", v), old)
	}
	f.fr.Paths["pyenv"] = "/fake/bin/pyenv"
	f.fr.On([]string{"pyenv", "version-name"}, "", errors.New("boom"))
	// rustup: the default cannot be read
	for _, v := range []string{"stable-x", "nightly-x", "1.60.0-x"} {
		mkdir(t, filepath.Join(f.path(".rustup"), "toolchains", v), old)
	}
	f.fr.On([]string{"rustup", "default"}, "", errors.New("boom"))
	cands, _ := f.scan(t, "toolchains")
	if len(cands) != 0 {
		t.Fatalf("candidates with unknown active version: %v", names(cands))
	}
}

func TestRustupDoubtsKeepAll(t *testing.T) {
	tc := "-aarch64-apple-darwin"
	setup := func(t *testing.T) (*fixture, string) {
		f := newFixture(t, "linux")
		for _, v := range []string{"stable" + tc, "nightly" + tc, "1.60.0" + tc} {
			mkdir(t, filepath.Join(f.path(".rustup"), "toolchains", v), old)
		}
		repo := testutil.MakeRepo(t, f.path("work", "app"))
		f.env.Roots = []core.Root{{Path: repo, Source: "config"}}
		f.fr.On([]string{"rustup", "default"}, "stable"+tc+" (default)\n", nil)
		return f, repo
	}
	// override list fails
	f, _ := setup(t)
	f.fr.On([]string{"rustup", "override", "list"}, "", errors.New("boom"))
	if cands, _ := f.scan(t, "toolchains"); len(cands) != 0 {
		t.Fatalf("override list failed: %v", names(cands))
	}
	// an override pins its toolchain
	f, _ = setup(t)
	f.fr.On([]string{"rustup", "override", "list"}, "/some/dir\tnightly"+tc+"\n", nil)
	if cands, _ := f.scan(t, "toolchains"); !slices.Equal(names(cands), []string{"1.60.0" + tc}) {
		t.Fatalf("override: %v", names(cands))
	}
	// RUSTUP_TOOLCHAIN pins too
	f, _ = setup(t)
	t.Setenv("RUSTUP_TOOLCHAIN", "1.60.0")
	f.fr.On([]string{"rustup", "override", "list"}, "no overrides\n", nil)
	if cands, _ := f.scan(t, "toolchains"); !slices.Equal(names(cands), []string{"nightly" + tc}) {
		t.Fatalf("RUSTUP_TOOLCHAIN: %v", names(cands))
	}
	t.Setenv("RUSTUP_TOOLCHAIN", "")
	// a rust-toolchain.toml that does not parse but has a channel
	f, repo := setup(t)
	f.fr.On([]string{"rustup", "override", "list"}, "no overrides\n", nil)
	write(t, filepath.Join(repo, "rust-toolchain.toml"), "[toolchain\nchannel = \"1.60.0\"\n")
	if cands, _ := f.scan(t, "toolchains"); len(cands) != 0 {
		t.Fatalf("broken toml: %v", names(cands))
	}
}

func TestNodePinsFromRoots(t *testing.T) {
	f := newFixture(t, "linux")
	nvm := f.path(".nvm")
	for _, v := range []string{"v16.0.0", "v18.19.0", "v20.11.0", "v22.1.0"} {
		mkdir(t, filepath.Join(nvm, "versions", "node", v), old)
	}
	write(t, filepath.Join(nvm, "alias", "default"), "v22.1.0")
	a := testutil.MakeRepo(t, f.path("work", "a"))
	b := testutil.MakeRepo(t, f.path("work", "b"))
	write(t, filepath.Join(a, ".nvmrc"), "v18.19.0\n")
	write(t, filepath.Join(b, ".tool-versions"), "ruby 3.2.0\nnodejs 16.0.0 # old\n")
	f.env.Roots = []core.Root{{Path: a}, {Path: b}}
	// 22 active, 20 the newest other, 18 and 16 pinned: nothing to remove
	if cands, _ := f.scan(t, "toolchains"); len(cands) != 0 {
		t.Fatalf("pinned versions proposed: %v", names(cands))
	}
	// an alias pin is a doubt
	write(t, filepath.Join(a, ".nvmrc"), "lts/*\n")
	if cands, _ := f.scan(t, "toolchains"); len(cands) != 0 {
		t.Fatalf("alias pin: %v", names(cands))
	}
	// pin 20 only: 22 active, 20 pinned, 18 the newest other; 16 goes
	write(t, filepath.Join(a, ".nvmrc"), "20\n")
	write(t, filepath.Join(b, ".tool-versions"), "ruby 3.2.0\n")
	if cands, _ := f.scan(t, "toolchains"); !slices.Equal(names(cands), []string{"v16.0.0"}) {
		t.Fatalf("candidates %v", names(cands))
	}
}

func TestUnreadablePinFileKeepsAll(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	f := newFixture(t, "linux")
	for _, v := range []string{"3.8.0", "3.9.0", "3.10.0", "3.11.0"} {
		mkdir(t, filepath.Join(f.path(".pyenv"), "versions", v), old)
	}
	write(t, filepath.Join(f.path(".pyenv"), "version"), "3.11.0\n")
	repo := testutil.MakeRepo(t, f.path("work", "app"))
	f.env.Roots = []core.Root{{Path: repo}}
	pin := filepath.Join(repo, ".python-version")
	write(t, pin, "3.8.0\n")
	if err := os.Chmod(pin, 0); err != nil {
		t.Fatal(err)
	}
	if cands, _ := f.scan(t, "toolchains"); len(cands) != 0 {
		t.Fatalf("unreadable pin file: %v", names(cands))
	}
}

func TestPythonPins(t *testing.T) {
	f := newFixture(t, "linux")
	root := f.path(".pyenv")
	for _, v := range []string{"3.8.0", "3.9.1", "3.10.4", "3.11.4", "3.12.0"} {
		mkdir(t, filepath.Join(root, "versions", v), old)
	}
	write(t, filepath.Join(root, "version"), "3.11.4\n")
	repo := testutil.MakeRepo(t, f.path("work", "app"))
	write(t, filepath.Join(repo, ".python-version"), "3.10.4\n")
	write(t, filepath.Join(repo, ".tool-versions"), "python 3.9\n")
	f.env.Roots = []core.Root{{Path: repo}}
	t.Setenv("PYENV_VERSION", "3.8.0")
	if cands, _ := f.scan(t, "toolchains"); len(cands) != 0 {
		t.Fatalf("pinned versions proposed: %v", names(cands))
	}
}

func TestPyenvSymlinkedActive(t *testing.T) {
	f := newFixture(t, "linux")
	root := f.path(".pyenv")
	for _, v := range []string{"3.8.0", "3.10.0", "3.12.1", "3.13.0"} {
		mkdir(t, filepath.Join(root, "versions", v), old)
	}
	mkdir(t, filepath.Join(root, "versions", "3.12.1", "envs", "myenv"), old)
	if err := os.Symlink(filepath.Join(root, "versions", "3.12.1", "envs", "myenv"), filepath.Join(root, "versions", "myenv")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "version"), "myenv\n")
	cands, _ := f.scan(t, "toolchains")
	// 3.12.1 (through the link) and 3.13.0 (newest other) stay
	if got := names(cands); !slices.Equal(got, []string{"3.10.0", "3.8.0"}) {
		t.Fatalf("candidates %v", got)
	}
	// a dangling link is a doubt
	if err := os.Remove(filepath.Join(root, "versions", "3.12.1", "envs", "myenv")); err != nil {
		t.Fatal(err)
	}
	if cands, _ := f.scan(t, "toolchains"); len(cands) != 0 {
		t.Fatalf("dangling active link: %v", names(cands))
	}
}

func TestNonVersionNamesNeverOffered(t *testing.T) {
	f := newFixture(t, "linux")
	root := f.path(".pyenv")
	for _, v := range []string{"3.8.0", "3.9.0", "3.12.0", "3.13.0", "default", "system", "current", "latest", "lts",
		".cache", "3.11.0.lock", "3.11.1.partial", "3.11.2.tmp", "tmp-3.11"} {
		mkdir(t, filepath.Join(root, "versions", v), old)
	}
	write(t, filepath.Join(root, "version"), "3.13.0\n")
	cands, _ := f.scan(t, "toolchains")
	if got := names(cands); !slices.Equal(got, []string{"3.8.0", "3.9.0"}) {
		t.Fatalf("candidates %v", got)
	}
}

func TestSimulatorPathStrictlyBelowBase(t *testing.T) {
	f := newFixture(t, "darwin")
	mkdir(t, f.path("Library", "Developer", "CoreSimulator", "Devices"), old)
	f.fr.Paths["xcrun"] = "/usr/bin/xcrun"
	f.fr.On([]string{"xcrun", "simctl", "list", "devices", "unavailable"}, "iPhone (ABC) (Shutdown) (unavailable, x)\n", nil)
	cands, _ := f.scan(t, "apple")
	if len(cands) != 1 {
		t.Fatalf("candidates %+v", cands)
	}
	c := cands[0]
	if c.Path == c.Base {
		t.Fatalf("path equals base: %s", c.Path)
	}
	home, err := filepath.EvalSymlinks(f.home)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Place(f.env.Platform, c.Base, c.Path); err != nil {
		t.Fatalf("Place: %v", err)
	}
	if err := guard.Deny(home, "", c.Path); err != nil {
		t.Fatalf("Deny: %v", err)
	}
}

func TestDerivedDataSkipsNoindex(t *testing.T) {
	f := newFixture(t, "darwin")
	dd := f.path("Library", "Developer", "Xcode", "DerivedData")
	mkdir(t, filepath.Join(dd, "ModuleCache.noindex"), old)
	mkdir(t, filepath.Join(dd, "SymbolCache.noindex"), old)
	mkdir(t, filepath.Join(dd, ".hidden"), old)
	mkdir(t, filepath.Join(dd, "OldApp-abc"), old)
	if cands, _ := f.scan(t, "apple"); !slices.Equal(names(cands), []string{"OldApp-abc"}) {
		t.Fatalf("candidates %v", names(cands))
	}
}

func TestDeviceSupportPerPlatformPrefix(t *testing.T) {
	f := newFixture(t, "darwin")
	ds := f.path("Library", "Developer", "Xcode", "iOS DeviceSupport")
	for _, v := range []string{"iPhone15,2 17.2 (21C1)", "iPhone15,2 17.1 (21B1)", "iPhone15,2 16.0 (20A1)",
		"iPad13,1 17.0 (21A1)", "iPad13,1 16.0 (20A2)", "Unparsed"} {
		mkdir(t, filepath.Join(ds, v), old)
	}
	cands, _ := f.scan(t, "apple")
	if got := names(cands); !slices.Equal(got, []string{"iPhone15,2 16.0 (20A1)"}) {
		t.Fatalf("candidates %v", got)
	}
}

func TestNoGoToolchainRule(t *testing.T) {
	if core.LocationKnown("go-toolchains") {
		t.Fatal("go-toolchains must not be a location: GOMODCACHE is never cleaned automatically")
	}
	for _, osName := range []string{"darwin", "linux"} {
		for _, r := range loadRules(t, osName) {
			if r.Location == "go-toolchains" {
				t.Fatalf("rule %s", r.Key())
			}
		}
	}
}

func TestPlaywrightKeepsNewestEach(t *testing.T) {
	f := newFixture(t, "linux")
	pw := f.path(".cache", "ms-playwright")
	for _, v := range []string{"chromium-1140", "chromium-1148", "firefox-1450", "webkit-1900", "webkit-2000", "ffmpeg-1010"} {
		mkdir(t, filepath.Join(pw, v), old)
	}
	write(t, filepath.Join(pw, ".links"), "x")
	cands, _ := f.scan(t, "browsers")
	if got := names(cands); !slices.Equal(got, []string{"chromium-1140", "webkit-1900"}) {
		t.Fatalf("candidates %v", got)
	}
	if err := (&versionsCat{name: "browsers"}).Proof(f.env, cands[0]); err != nil {
		t.Fatalf("proof: %v", err)
	}
}

func TestPlaywrightMacPath(t *testing.T) {
	f := newFixture(t, "darwin")
	pw := f.path("Library", "Caches", "ms-playwright")
	mkdir(t, filepath.Join(pw, "chromium-1", "x"), old)
	mkdir(t, filepath.Join(pw, "chromium-1"), old)
	mkdir(t, filepath.Join(pw, "chromium-2"), old)
	if cands, _ := f.scan(t, "browsers"); !slices.Equal(names(cands), []string{"chromium-1"}) {
		t.Fatalf("candidates %v", names(cands))
	}
}

func TestDerivedDataOld(t *testing.T) {
	f := newFixture(t, "darwin")
	dd := f.path("Library", "Developer", "Xcode", "DerivedData")
	mkdir(t, filepath.Join(dd, "OldApp-abc"), 60*24*time.Hour)
	mkdir(t, filepath.Join(dd, "NewApp-def"), 2*24*time.Hour)
	cands, kept := f.scan(t, "apple")
	if got := names(cands); !slices.Equal(got, []string{"OldApp-abc"}) {
		t.Fatalf("candidates %v", got)
	}
	if cands[0].Removal != core.RenameDelete || cands[0].Base != dd {
		t.Fatalf("candidate %+v", cands[0])
	}
	if len(kept) != 1 || kept[0].Why != "young" {
		t.Fatalf("kept %v", kept)
	}
	if err := (&appleCat{}).Proof(f.env, cands[0]); err != nil {
		t.Fatalf("proof: %v", err)
	}
}

func TestDeviceSupportKeepsNewestTwo(t *testing.T) {
	f := newFixture(t, "darwin")
	ds := f.path("Library", "Developer", "Xcode", "iOS DeviceSupport")
	for _, v := range []string{"15.7 (19H12)", "16.4 (20E247)", "17.2.1 (21C66)", "17.0 (21A329)", "weird"} {
		mkdir(t, filepath.Join(ds, v), old)
	}
	cands, _ := f.scan(t, "apple")
	if got := names(cands); !slices.Equal(got, []string{"15.7 (19H12)", "16.4 (20E247)"}) {
		t.Fatalf("candidates %v", got)
	}
}

func TestSimulatorsOnlyWhenUnavailableListed(t *testing.T) {
	f := newFixture(t, "darwin")
	mkdir(t, f.path("Library", "Developer", "CoreSimulator", "Devices"), old)
	f.fr.Paths["xcrun"] = "/usr/bin/xcrun"
	f.fr.On([]string{"xcrun", "simctl", "list", "devices", "unavailable"}, "== Devices ==\n", nil)
	if cands, _ := f.scan(t, "apple"); len(cands) != 0 {
		t.Fatalf("candidates without unavailable devices: %v", cands)
	}
	f.fr.On([]string{"xcrun", "simctl", "list", "devices", "unavailable"},
		"== Devices ==\n-- iOS 15.0 --\n    iPhone 12 (ABC) (Shutdown) (unavailable, runtime profile not found)\n", nil)
	cands, _ := f.scan(t, "apple")
	if len(cands) != 1 || cands[0].Removal != core.ToolCommand ||
		!slices.Equal(cands[0].Command, []string{"xcrun", "simctl", "delete", "unavailable"}) {
		t.Fatalf("candidates %+v", cands)
	}
	if !core.ToolActionKnown("xcrun", "simctl-delete-unavailable") {
		t.Fatal("xcrun action not registered")
	}
}

func TestAppleSkippedOnLinux(t *testing.T) {
	f := newFixture(t, "linux")
	mkdir(t, filepath.Join(f.path("Library", "Developer", "Xcode", "DerivedData"), "OldApp-abc"), old)
	f.fr.Paths["xcrun"] = "/usr/bin/xcrun"
	cands, kept := f.scan(t, "apple")
	if len(cands) != 0 || len(kept) != 0 {
		t.Fatalf("apple on linux: %v %v", cands, kept)
	}
}

func TestPackRulesResolve(t *testing.T) {
	rules := loadRules(t, "darwin") // the full set; apple rules exist only for darwin
	var n int
	for _, r := range rules {
		if r.Category == "toolchains" || r.Category == "browsers" || r.Category == "apple" {
			n++
		}
	}
	if n < 9 {
		t.Fatalf("only %d rules loaded", n)
	}
}
