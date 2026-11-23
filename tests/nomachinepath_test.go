package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	usersRe = regexp.MustCompile(`/Users/([^/\s<>"'` + "`" + `*$]+)`)
	homeRe  = regexp.MustCompile(`/home/([^/\s<>"'` + "`" + `*$]+)`)
	wordRe  = regexp.MustCompile(`\bTADA\b|\.agents/`)
	owner   = "alam" + "bari" // split so this file never names the owner itself
	allowed = []string{}      // the owner's OS user name is never allowed in a product file (the GitHub owner is als0x)
)

var binaryExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
	".pdf": true, ".zip": true, ".gz": true, ".tgz": true, ".woff": true, ".woff2": true}

func scanContent(content string) []string {
	var bad []string
	for _, m := range usersRe.FindAllStringSubmatch(content, -1) {
		if m[1] != "runner" {
			bad = append(bad, m[0])
		}
	}
	for _, m := range homeRe.FindAllStringSubmatch(content, -1) {
		if m[1] != "user" && m[1] != "runner" {
			bad = append(bad, m[0])
		}
	}
	bad = append(bad, wordRe.FindAllString(content, -1)...)
	stripped := content
	for _, a := range allowed {
		stripped = strings.ReplaceAll(stripped, a, "")
	}
	if strings.Contains(strings.ToLower(stripped), owner) {
		bad = append(bad, "owner user name")
	}
	return bad
}

// internalDocs are the design and planning notes: ignored by git, never published, and allowed to name the
// author's machine. The user guides directly in docs/ (packs.md, wsl.md, known-limits.md) are published and scanned.
var internalDocs = []string{"docs/specs/", "docs/plans/", "docs/prd/", "docs/reviews/", "docs/spikes/", "docs/scenarios/"}

func isInternalDoc(slash string) bool {
	for _, p := range internalDocs {
		if strings.HasPrefix(slash, p) {
			return true
		}
	}
	return false
}

// scanMachinePaths scans files (relative to root) other than the internal notes and returns "file: match" findings.
func scanMachinePaths(root string, files []string) []string {
	var found []string
	for _, rel := range files {
		slash := filepath.ToSlash(rel)
		if isInternalDoc(slash) || strings.HasPrefix(slash, ".git/") ||
			strings.HasPrefix(slash, ".worktrees/") || slash == "tests/nomachinepath_test.go" {
			continue
		}
		if binaryExt[strings.ToLower(filepath.Ext(slash))] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			found = append(found, slash+": read error: "+err.Error())
			continue
		}
		for _, m := range scanContent(string(b)) {
			found = append(found, slash+": "+m)
		}
	}
	return found
}

func TestNoMachinePath(t *testing.T) {
	root := repoRoot()
	for _, f := range scanMachinePaths(root, listFiles(root)) {
		t.Error(f)
	}
}

func TestScannerFindsPlantedPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("p := \"/Users/someone/x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.md"), []byte("/Users/runner /home/user /Users/<name>/x github.com/als0x/sapulidi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := scanMachinePaths(dir, []string{"x.go", "ok.md"})
	if len(got) != 1 || !strings.HasPrefix(got[0], "x.go: /Users/someone") {
		t.Fatalf("got %v", got)
	}
	// The old GitHub owner must not slip back in (the repository moved to als0x).
	for _, s := range []string{"/home/bob/x", "uses TADA here", "see .agents/DISK.md", "hi " + owner,
		"github.com/" + owner + "/sapulidi", owner + "-sapulidi"} {
		if len(scanContent(s)) == 0 {
			t.Errorf("%q not flagged", s)
		}
	}
}

// repoRoot is the directory above tests/.
func repoRoot() string {
	d, err := filepath.Abs("..")
	if err != nil {
		panic(err)
	}
	return d
}

// listFiles returns the repository's files relative to root: tracked or new, not ignored,
// when git can tell; else a walk that skips .git, .worktrees, .chief and .claude.
func listFiles(root string) []string {
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	if out, err := cmd.Output(); err == nil && len(out) > 0 {
		var files []string
		for _, f := range strings.Split(string(out), "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
		sort.Strings(files)
		return files
	}
	var files []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", ".chief", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		files = append(files, rel)
		return nil
	})
	sort.Strings(files)
	return files
}

func TestListFilesIncludesUntracked(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := listFiles(dir)
	if len(got) != 1 || got[0] != "new.txt" {
		t.Fatalf("got %v", got)
	}
}
