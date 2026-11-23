package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	usesRe    = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*(\S+)(.*)$`)
	pinnedRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._/-]+@[0-9a-f]{40}$`)
	commentRe = regexp.MustCompile(`^\s+#\s*v?\d+(\.\d+)*\S*`)
)

// unpinnedUses returns the problems found in one workflow file's text.
func unpinnedUses(text string) []string {
	var bad []string
	for _, line := range strings.Split(text, "\n") {
		m := usesRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if strings.HasPrefix(m[1], "./") {
			continue
		}
		if !pinnedRe.MatchString(m[1]) {
			bad = append(bad, line+" (not pinned by a 40-hex sha)")
		} else if !commentRe.MatchString(m[2]) {
			bad = append(bad, line+" (no version comment)")
		}
	}
	return bad
}

func TestWorkflowActionsArePinned(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot(), ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no workflow found")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range unpinnedUses(string(b)) {
			t.Errorf("%s: %s", filepath.Base(f), strings.TrimSpace(p))
		}
	}
}

func TestUnpinnedUsesFindsProblems(t *testing.T) {
	sha := strings.Repeat("a", 40)
	ok := "      - uses: actions/checkout@" + sha + " # v4.2.2\n"
	if got := unpinnedUses(ok); len(got) != 0 {
		t.Fatalf("pinned line flagged: %v", got)
	}
	for _, s := range []string{
		"      - uses: actions/checkout@v4\n",
		"      - uses: actions/checkout@" + sha[:39] + "\n",
		"      - uses: actions/checkout@" + sha + "\n",
	} {
		if len(unpinnedUses(s)) != 1 {
			t.Errorf("not flagged: %q", s)
		}
	}
}
