package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/testutil"
)

var (
	codeSpanRe = regexp.MustCompile("`([^`\n]+)`")
	cmdRe      = regexp.MustCompile(`^sapulidi (?:--json )?([a-z][a-z-]*)`)
	flagRe     = regexp.MustCompile(`--[a-z][a-z-]*`)
)

// registeredCommands builds the real binary and returns the command names its help lists.
func registeredCommands(t *testing.T) map[string]bool {
	t.Helper()
	testutil.TempHome(t)
	bin := filepath.Join(t.TempDir(), "sapulidi")
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", bin, "./cmd/sapulidi")
	build.Dir = repoRoot()
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "help").Output()
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	cmds := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "  ") {
			if f := strings.Fields(line); len(f) > 0 {
				cmds[f[0]] = true
			}
		}
	}
	return cmds
}

// commandSource returns the text of the non-test source file that registers the command.
func commandSource(t *testing.T, name string) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(), "internal", "cli", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	needle := `Name: "` + name + `"`
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), needle) {
			return string(b)
		}
	}
	return ""
}

// declaresFlag reports whether the command source declares the flag: a flag-set declaration such as
// fs.Bool("yes", ...) or fs.StringVar(&v, "host", ...), or a hand-parsed argument compared with "--name".
func declaresFlag(src, name string) bool {
	q := regexp.QuoteMeta(name)
	decl := regexp.MustCompile(`\.(?:Bool|String|Int|Int64|Uint|Float64|Duration)(?:Var)?\(\s*(?:&[\w.]+,\s*)?"` + q + `"`)
	hand := regexp.MustCompile(`==\s*"--` + q + `"`)
	return decl.MatchString(src) || hand.MatchString(src)
}

// mentionedCommands returns every "<command> [flags]" named in a backticked span of the text.
func mentionedCommands(text string) []struct {
	Cmd   string
	Flags []string
} {
	var out []struct {
		Cmd   string
		Flags []string
	}
	for _, span := range codeSpanRe.FindAllStringSubmatch(text, -1) {
		m := cmdRe.FindStringSubmatch(span[1])
		if m == nil {
			continue
		}
		out = append(out, struct {
			Cmd   string
			Flags []string
		}{m[1], flagRe.FindAllString(span[1], -1)})
	}
	return out
}

func TestSkillCommandsAreRegistered(t *testing.T) {
	cmds := registeredCommands(t)
	files, err := filepath.Glob(filepath.Join(repoRoot(), "skills", "sapulidi", "references", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join(repoRoot(), "skills", "sapulidi", "SKILL.md"))
	seen := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(repoRoot(), f)
		for _, m := range mentionedCommands(string(b)) {
			seen++
			if !cmds[m.Cmd] {
				t.Errorf("%s names `sapulidi %s`, which the binary does not list in help", rel, m.Cmd)
				continue
			}
			src := commandSource(t, m.Cmd)
			for _, fl := range m.Flags {
				name := strings.TrimPrefix(fl, "--")
				if name == "json" { // global, accepted by every command
					continue
				}
				if !declaresFlag(src, name) {
					t.Errorf("%s names `sapulidi %s %s`, but the command does not accept %s", rel, m.Cmd, fl, fl)
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("no sapulidi command found in the skill files; the extraction is broken")
	}
}

func TestMentionedCommandsFindsUnknown(t *testing.T) {
	got := mentionedCommands("run `sapulidi frobnicate --yes` and `sapulidi check --host codex`, not sapulidi prose, nor `the sapulidi binary`")
	if len(got) != 2 || got[0].Cmd != "frobnicate" || got[1].Cmd != "check" || len(got[1].Flags) != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestDeclaresFlagOnlyForRealDeclarations(t *testing.T) {
	src := "yes := fs.Bool(\"yes\", false, \"x\")\nfs.StringVar(&h, \"host\", \"\", \"x\")\nif a == \"--all\" {}\nlog(\"quick\")"
	for _, f := range []string{"yes", "host", "all"} {
		if !declaresFlag(src, f) {
			t.Errorf("%s not found", f)
		}
	}
	if declaresFlag(src, "quick") {
		t.Error("a string that is not a declaration counted as a flag")
	}
}
