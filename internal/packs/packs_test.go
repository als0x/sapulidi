package packsload

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/als0x/sapulidi/internal/core"
)

type fakeCat struct {
	name string
	b    core.Bucket
}

func (f fakeCat) Name() string                                          { return f.name }
func (f fakeCat) Bucket() core.Bucket                                   { return f.b }
func (f fakeCat) Scan(*core.Env) ([]core.Candidate, []core.Kept, error) { return nil, nil, nil }
func (f fakeCat) Proof(*core.Env, core.Candidate) error                 { return nil }

func TestMain(m *testing.M) {
	core.Register(fakeCat{"pk-temp", core.Auto})
	core.Register(fakeCat{"pk-deps", core.Optional})
	core.Register(fakeCat{"pk-models", core.Never})
	core.RegisterToolAction("pk-go", "clean-cache")
	core.RegisterStrategy("pk-strategy")
	core.RegisterLocation("pk-location")
	os.Exit(m.Run())
}

const validPack = `pack = "go"
[[rule]]
id = "go-build"
rev = 1
category = "pk-temp"
bucket = "auto"
kind = "temp"
names = ["go-build*"]
min_age = "1h"
`

func rule(extra string) string {
	return "pack = \"p\"\n[[rule]]\nid = \"r\"\nrev = 1\n" + extra
}

func loadOne(t *testing.T, name, content string) ([]core.Rule, []error) {
	t.Helper()
	return load(fstest.MapFS{name + ".toml": {Data: []byte(content)}}, "", "linux")
}

func wantErr(t *testing.T, errs []error, parts ...string) {
	t.Helper()
	if len(errs) == 0 {
		t.Fatalf("no error, want one with %v", parts)
	}
	for _, e := range errs {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(e.Error(), p)
		}
		if ok {
			return
		}
	}
	t.Fatalf("errors %v lack %v", errs, parts)
}

func TestLoadValidPack(t *testing.T) {
	rules, errs := loadOne(t, "go", validPack)
	if len(errs) != 0 || len(rules) != 1 {
		t.Fatalf("rules %v errs %v", rules, errs)
	}
	if rules[0].Key() != "go/go-build@1" || rules[0].MinAge.Hours() != 1 || rules[0].Kind != core.KindTemp {
		t.Fatalf("got %+v", rules[0])
	}
}

func TestPackNameMustMatchFile(t *testing.T) {
	rules, errs := loadOne(t, "other", validPack)
	if len(rules) != 0 {
		t.Fatal("pack loaded")
	}
	wantErr(t, errs, "pack other", "field pack")
}

func TestDuplicateIDRejected(t *testing.T) {
	rules, errs := loadOne(t, "go", validPack+strings.TrimPrefix(validPack, "pack = \"go\"\n"))
	if len(rules) != 0 {
		t.Fatal("pack loaded")
	}
	wantErr(t, errs, "pack go", "rule go-build", "field id")
}

func TestUnknownKeyRejectsPack(t *testing.T) {
	rules, errs := loadOne(t, "go", validPack+"path = \"/etc\"\n")
	if len(rules) != 0 {
		t.Fatal("pack loaded")
	}
	wantErr(t, errs, "pack go", "rule.path", "unknown key")
	_, errs = loadOne(t, "go", validPack+"[[rule]]\nid = \"b\"\nrev = 1\ncategory = \"pk-temp\"\nbucket = \"auto\"\nkind = \"temp\"\nnames = [\"xyz*\"]\ncommand = \"rm\"\n")
	wantErr(t, errs, "unknown key")
}

func TestInvalidRuleDropsWholePack(t *testing.T) {
	bad := validPack + "[[rule]]\nid = \"Bad_ID\"\nrev = 1\ncategory = \"pk-temp\"\nbucket = \"auto\"\nkind = \"temp\"\nnames = [\"xyz*\"]\n"
	rules, errs := loadOne(t, "go", bad)
	if len(rules) != 0 {
		t.Fatalf("rules of an invalid pack loaded: %v", rules)
	}
	wantErr(t, errs, "field id")
}

func TestNamesRejectSlashDotDotAndStar(t *testing.T) {
	for _, n := range []string{"a/b", "..", "*", ".*", ".git", "src", "lib", "app", "docs", "test", "tests", "**", "s*", ".GIT", "SRC", "[", ""} {
		body := rule("category = \"pk-temp\"\nbucket = \"auto\"\nkind = \"temp\"\nnames = [" + quote(n) + "]\n")
		rules, errs := loadOne(t, "p", body)
		if len(rules) != 0 {
			t.Errorf("name %q accepted", n)
		}
		wantErr(t, errs, "field names")
	}
	for _, n := range []string{"go-build*", "jest_*", "dist", "*.log", "node_modules"} {
		body := rule("category = \"pk-temp\"\nbucket = \"auto\"\nkind = \"temp\"\nnames = [" + quote(n) + "]\n")
		if _, errs := loadOne(t, "p", body); len(errs) != 0 {
			t.Errorf("name %q rejected: %v", n, errs)
		}
	}
}

func quote(s string) string { return "\"" + strings.ReplaceAll(s, "\\", "\\\\") + "\"" }

func TestBucketMustMatchCategory(t *testing.T) {
	_, errs := loadOne(t, "p", rule("category = \"pk-temp\"\nbucket = \"optional\"\nkind = \"temp\"\nnames = [\"xyz*\"]\n"))
	wantErr(t, errs, "rule r", "field bucket")
	_, errs = loadOne(t, "p", rule("category = \"pk-deps\"\nbucket = \"auto\"\nkind = \"temp\"\nnames = [\"xyz*\"]\n"))
	wantErr(t, errs, "field bucket")
	// never may lower any rule; a never category only takes never.
	for _, c := range []string{"pk-temp", "pk-deps", "pk-models"} {
		if _, errs = loadOne(t, "p", rule("category = \""+c+"\"\nbucket = \"never\"\nkind = \"temp\"\nnames = [\"xyz*\"]\n")); len(errs) != 0 {
			t.Errorf("never in %s: %v", c, errs)
		}
	}
	_, errs = loadOne(t, "p", rule("category = \"pk-models\"\nbucket = \"auto\"\nkind = \"temp\"\nnames = [\"xyz*\"]\n"))
	wantErr(t, errs, "field bucket")
	_, errs = loadOne(t, "p", rule("category = \"nope\"\nbucket = \"auto\"\nkind = \"temp\"\nnames = [\"xyz*\"]\n"))
	wantErr(t, errs, "field category")
}

func TestMinAgeBelowBucketMinimum(t *testing.T) {
	cases := []struct{ cat, bucket, age string }{
		{"pk-temp", "auto", "59m"}, {"pk-deps", "optional", "23h"}, {"pk-temp", "auto", "0s"},
	}
	for _, c := range cases {
		_, errs := loadOne(t, "p", rule("category = \""+c.cat+"\"\nbucket = \""+c.bucket+"\"\nkind = \"temp\"\nnames = [\"xyz*\"]\nmin_age = \""+c.age+"\"\n"))
		wantErr(t, errs, "field min_age")
	}
	for _, age := range []string{"30d", "24h"} {
		if _, errs := loadOne(t, "p", rule("category = \"pk-deps\"\nbucket = \"optional\"\nkind = \"temp\"\nnames = [\"xyz*\"]\nmin_age = \""+age+"\"\n")); len(errs) != 0 {
			t.Errorf("age %s: %v", age, errs)
		}
	}
	_, errs := loadOne(t, "p", rule("category = \"pk-temp\"\nbucket = \"auto\"\nkind = \"temp\"\nnames = [\"xyz*\"]\nmin_age = \"soon\"\n"))
	wantErr(t, errs, "field min_age")
	// absent min_age defaults to the bucket minimum.
	rules, errs := loadOne(t, "p", rule("category = \"pk-deps\"\nbucket = \"optional\"\nkind = \"temp\"\nnames = [\"xyz*\"]\n"))
	if len(errs) != 0 || rules[0].MinAge.Hours() != 24 {
		t.Fatalf("default age: %v %v", rules, errs)
	}
}

func TestProjectNeedsMarkers(t *testing.T) {
	base := "category = \"pk-deps\"\nbucket = \"optional\"\nkind = \"project\"\nnames = [\"node_modules\"]\n"
	_, errs := loadOne(t, "p", rule(base))
	wantErr(t, errs, "field markers")
	_, errs = loadOne(t, "p", rule(base+"markers = [\"src\"]\n"))
	wantErr(t, errs, "field markers")
	if _, errs = loadOne(t, "p", rule(base+"markers = [\"package.json\"]\n")); len(errs) != 0 {
		t.Fatal(errs)
	}
	_, errs = loadOne(t, "p", rule("category = \"pk-deps\"\nbucket = \"optional\"\nkind = \"file\"\nnames = [\"*.log\"]\n"))
	wantErr(t, errs, "field markers")
}

func TestCacheNeedsKnownToolAction(t *testing.T) {
	base := "category = \"pk-deps\"\nbucket = \"optional\"\nkind = \"cache\"\n"
	for _, extra := range []string{"", "tool = \"pk-go\"\naction = \"rm-rf\"\n", "tool = \"sh\"\naction = \"clean-cache\"\n"} {
		_, errs := loadOne(t, "p", rule(base+extra))
		wantErr(t, errs, "field tool")
	}
	if _, errs := loadOne(t, "p", rule(base+"tool = \"pk-go\"\naction = \"clean-cache\"\n")); len(errs) != 0 {
		t.Fatal(errs)
	}
}

func TestVersionsNeedsKnownStrategyLocation(t *testing.T) {
	base := "category = \"pk-deps\"\nbucket = \"optional\"\nkind = \"versions\"\n"
	_, errs := loadOne(t, "p", rule(base+"strategy = \"nope\"\nlocation = \"pk-location\"\n"))
	wantErr(t, errs, "field strategy")
	_, errs = loadOne(t, "p", rule(base+"strategy = \"pk-strategy\"\nlocation = \"nope\"\n"))
	wantErr(t, errs, "field location")
	if _, errs = loadOne(t, "p", rule(base+"strategy = \"pk-strategy\"\nlocation = \"pk-location\"\n")); len(errs) != 0 {
		t.Fatal(errs)
	}
	path := "category = \"pk-deps\"\nbucket = \"optional\"\nkind = \"path\"\n"
	_, errs = loadOne(t, "p", rule(path+"location = \"nope\"\n"))
	wantErr(t, errs, "field location")
	if _, errs = loadOne(t, "p", rule(path+"location = \"pk-location\"\n")); len(errs) != 0 {
		t.Fatal(errs)
	}
}

func TestKindAndOSValidated(t *testing.T) {
	_, errs := loadOne(t, "p", rule("category = \"pk-temp\"\nbucket = \"auto\"\nkind = \"exec\"\n"))
	wantErr(t, errs, "field kind")
	_, errs = loadOne(t, "p", rule("category = \"pk-temp\"\nbucket = \"auto\"\nkind = \"temp\"\nnames = [\"xyz*\"]\nos = [\"windows\"]\n"))
	wantErr(t, errs, "field os")
	_, errs = loadOne(t, "p", strings.Replace(rule("category = \"pk-temp\"\nbucket = \"auto\"\nkind = \"temp\"\nnames = [\"xyz*\"]\n"), "rev = 1", "rev = 0", 1))
	wantErr(t, errs, "field rev")
}

func writeUser(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUserPackPrefixedAndCannotShadowBuiltin(t *testing.T) {
	dir := t.TempDir()
	writeUser(t, dir, "mine.toml", strings.ReplaceAll(validPack, `"go"`, `"mine"`))
	writeUser(t, dir, "go.toml", validPack)
	writeUser(t, dir, "notes.txt", "ignored")
	builtin := fstest.MapFS{"go.toml": {Data: []byte(validPack)}}
	rules, errs := load(builtin, dir, "linux")
	if len(rules) != 2 {
		t.Fatalf("rules %v", rules)
	}
	if rules[0].Pack != "go" || rules[1].Pack != "user-mine" || rules[1].Key() != "user-mine/go-build@1" {
		t.Fatalf("packs %q %q", rules[0].Pack, rules[1].Pack)
	}
	wantErr(t, errs, "pack go", "built-in")
}

func TestUserPackSymlinkIgnored(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	writeUser(t, other, "real.toml", strings.ReplaceAll(validPack, `"go"`, `"link"`))
	if err := os.Symlink(filepath.Join(other, "real.toml"), filepath.Join(dir, "link.toml")); err != nil {
		t.Fatal(err)
	}
	rules, errs := load(nil, dir, "linux")
	if len(rules) != 0 {
		t.Fatalf("symlinked pack loaded: %v", rules)
	}
	wantErr(t, errs, "pack link")
}

func TestUserDirMissingIsNotAnError(t *testing.T) {
	if rules, errs := load(nil, filepath.Join(t.TempDir(), "none"), "linux"); len(rules)+len(errs) != 0 {
		t.Fatal(rules, errs)
	}
}

func TestOSFilter(t *testing.T) {
	pack := validPack + "os = [\"darwin\"]\n" +
		"[[rule]]\nid = \"both\"\nrev = 1\ncategory = \"pk-temp\"\nbucket = \"auto\"\nkind = \"temp\"\nnames = [\"bbb*\"]\n"
	fsys := fstest.MapFS{"go.toml": {Data: []byte(pack)}}
	rules, errs := load(fsys, "", "linux")
	if len(errs) != 0 || len(rules) != 1 || rules[0].ID != "both" {
		t.Fatalf("linux: %v %v", rules, errs)
	}
	rules, _ = load(fsys, "", "darwin")
	if len(rules) != 2 {
		t.Fatalf("darwin: %v", rules)
	}
}

func patRule(kind, extra string) string {
	return rule("category = \"pk-temp\"\nbucket = \"auto\"\nkind = \"" + kind + "\"\n" + extra)
}

func TestPatternsRejectCaseClassAndBroad(t *testing.T) {
	for _, n := range []string{"S*", "[S]rc", ".G*", "SRC", "Src", ".Git", "[sS]rc", "?*", "*?*", "[!x]*", "*a*", "ab*", "????*", ".",
		"a b*", "ab\tcd*", "x\x00yz", "~/abc", "~abc", "a*b*c*d*eee", strings.Repeat("a", 65), "T[eE]st", "Doc?"} {
		if _, errs := loadOne(t, "p", patRule("temp", "names = ["+quote(n)+"]\n")); len(errs) == 0 {
			t.Errorf("name %q accepted", n)
		}
		if _, errs := loadOne(t, "p", patRule("project", "names = [\"dist\"]\nmarkers = ["+quote(n)+"]\n")); len(errs) == 0 {
			t.Errorf("marker %q accepted", n)
		}
	}
}

func TestBuiltinNamesStillPass(t *testing.T) {
	names := []string{"node_modules", ".next", ".nuxt", ".turbo", ".parcel-cache", ".svelte-kit", "dist", "__pycache__",
		".pytest_cache", ".mypy_cache", ".ruff_cache", ".hypothesis", ".venv", "venv", ".tox", ".nox", "build", "*.egg-info",
		"test-results", "playwright-report", "coverage", ".nyc_output", "htmlcov", "target", ".gradle", "bin", "obj",
		"hs_err_pid*.log", "*.hprof", "core", "core.[0-9]*", ".codegraph", ".serena", "vendor", ".bundle", ".dart_tool",
		"_build", "deps", "cmake-build-*", ".zig-cache", "zig-out", ".build", "Pods", "go-build*", "pytest-of-*", "pip-*",
		"playwright*", "vitest*", "jest_*", "tsx-*", "bunx-*", "node-compile-cache",
		"package.json", "go.mod", "Cargo.toml", "pom.xml", "build.gradle*", "*.csproj", "*.py", "requirements*.txt"}
	for _, n := range names {
		if err := checkPattern(n); err != nil {
			t.Errorf("%q rejected: %v", n, err)
		}
	}
}

func TestUserDirChecked(t *testing.T) {
	good := func(dir string) {
		writeUser(t, dir, "mine.toml", strings.ReplaceAll(validPack, `"go"`, `"mine"`))
	}
	real := t.TempDir()
	good(real)
	link := filepath.Join(t.TempDir(), "packs")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if rules, errs := load(nil, link, "linux"); len(rules) != 0 || len(errs) == 0 {
		t.Fatalf("symlinked dir: %v %v", rules, errs)
	}
	wr := t.TempDir()
	good(wr)
	if err := os.Chmod(wr, 0o775); err != nil {
		t.Fatal(err)
	}
	if rules, errs := load(nil, wr, "linux"); len(rules) != 0 || len(errs) == 0 {
		t.Fatalf("group-writable dir: %v %v", rules, errs)
	}
	if err := os.Chmod(wr, 0o755); err != nil {
		t.Fatal(err)
	}
	if rules, errs := load(nil, wr, "linux"); len(rules) != 1 || len(errs) != 0 {
		t.Fatalf("0755 dir: %v %v", rules, errs)
	}
	file := filepath.Join(t.TempDir(), "f")
	writeUser(t, filepath.Dir(file), "f", "x")
	if rules, errs := load(nil, file, "linux"); len(rules) != 0 || len(errs) == 0 {
		t.Fatalf("file as dir: %v %v", rules, errs)
	}
}

func TestUserPackFIFOAndSwapAndSize(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "x.toml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := readUserFile(fifo); err == nil {
			t.Error("fifo read")
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("fifo hangs the loader")
	}
	// a pack replaced by a symlink after listing: readUserFile on the path refuses it
	target := filepath.Join(t.TempDir(), "t.toml")
	writeUser(t, filepath.Dir(target), "t.toml", validPack)
	swapped := filepath.Join(dir, "swap.toml")
	if err := os.Symlink(target, swapped); err != nil {
		t.Fatal(err)
	}
	if _, err := readUserFile(swapped); err == nil {
		t.Fatal("symlink read")
	}
	big := filepath.Join(dir, "big.toml")
	if err := os.WriteFile(big, []byte("# "+strings.Repeat("x", 1<<20)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readUserFile(big); err == nil {
		t.Fatal("over 1 MiB read")
	}
}

func TestMinAgeOverflowRejected(t *testing.T) {
	for _, age := range []string{"9999999999999d", "106751991167301d", "3651d", "100000h", "-1d", "xd"} {
		_, errs := loadOne(t, "p", rule("category = \"pk-deps\"\nbucket = \"optional\"\nkind = \"temp\"\nnames = [\"x*y*z\"]\nmin_age = \""+age+"\"\n"))
		wantErr(t, errs, "field min_age")
	}
	if d, err := parseAge("3650d"); err != nil || d.Hours() != 3650*24 {
		t.Fatal(d, err)
	}
}

func TestFieldNotAllowedForKind(t *testing.T) {
	cases := map[string]string{
		"markers":  patRule("temp", "names = [\"abc*\"]\nmarkers = [\"go.mod\"]\n"),
		"tool":     patRule("temp", "names = [\"abc*\"]\ntool = \"pk-go\"\naction = \"clean-cache\"\n"),
		"action":   patRule("temp", "names = [\"abc*\"]\naction = \"clean-cache\"\n"),
		"strategy": patRule("temp", "names = [\"abc*\"]\nstrategy = \"pk-strategy\"\n"),
		"location": patRule("temp", "names = [\"abc*\"]\nlocation = \"pk-location\"\n"),
		"names":    patRule("cache", "names = [\"abc*\"]\ntool = \"pk-go\"\naction = \"clean-cache\"\n"),
	}
	for field, body := range cases {
		_, errs := loadOne(t, "p", body)
		wantErr(t, errs, "field "+field)
	}
	// a never rule still follows its kind
	_, errs := loadOne(t, "p", rule("category = \"pk-models\"\nbucket = \"never\"\nkind = \"temp\"\nnames = [\"abc*\"]\nmarkers = [\"go.mod\"]\n"))
	wantErr(t, errs, "field markers")
}
