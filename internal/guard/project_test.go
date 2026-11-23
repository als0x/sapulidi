package guard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/testutil"
)

// projectRepo returns a repo with a go.mod marker, an ignored build/ directory holding a file.
func projectRepo(t *testing.T) (repo, dir string) {
	t.Helper()
	isolateGit(t)
	repo = testutil.MakeRepo(t, filepath.Join(realDir(t), "repo"))
	write(t, filepath.Join(repo, "go.mod"), "module x\n")
	write(t, filepath.Join(repo, ".gitignore"), "build/\n")
	git(t, repo, "add", "go.mod", ".gitignore")
	git(t, repo, "commit", "-m", "marker")
	dir = filepath.Join(repo, "build")
	write(t, filepath.Join(dir, "out.bin"), "x")
	return repo, dir
}

var markers = []string{"go.mod", "*.csproj"}

func TestProjectAcceptsIgnoredArtifact(t *testing.T) {
	_, dir := projectRepo(t)
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "")
}

func TestProjectNoMarker(t *testing.T) {
	repo, dir := projectRepo(t)
	if err := os.Remove(filepath.Join(repo, "go.mod")); err != nil {
		t.Fatal(err)
	}
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "no-marker")
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, nil), "no-marker")
}

func TestProjectNotGit(t *testing.T) {
	isolateGit(t)
	parent := realDir(t)
	write(t, filepath.Join(parent, "go.mod"), "x")
	write(t, filepath.Join(parent, "build", "f"), "x")
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(parent))
	wantWhy(t, ProjectArtifact(execx.Real{}, filepath.Join(parent, "build"), markers), "not-ignored")
}

func TestProjectNotIgnored(t *testing.T) {
	repo, _ := projectRepo(t)
	dir := filepath.Join(repo, "handmade")
	write(t, filepath.Join(dir, "f"), "x")
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "not-ignored")
}

func TestProjectTrackedFileInside(t *testing.T) {
	repo, dir := projectRepo(t)
	git(t, repo, "add", "-f", "build/out.bin")
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "tracked")
}

func TestProjectNestedGit(t *testing.T) {
	_, dir := projectRepo(t)
	write(t, filepath.Join(dir, "sub", "deep", ".git", "HEAD"), "ref: x")
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "nested-git")
}

func TestProjectLinkedWorktreeParent(t *testing.T) {
	repo, _ := projectRepo(t)
	wt := filepath.Join(realDir(t), "wt")
	git(t, repo, "worktree", "add", "-b", "feat", wt)
	dir := filepath.Join(wt, "build")
	write(t, filepath.Join(dir, "f"), "x")
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "not-ignored")
}

func TestProjectRefusesSymlinkDir(t *testing.T) {
	repo, dir := projectRepo(t)
	link := filepath.Join(repo, "build2")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	wantWhy(t, ProjectArtifact(execx.Real{}, link, markers), "not-ignored")
	write(t, filepath.Join(repo, "afile"), "x")
	wantWhy(t, ProjectArtifact(execx.Real{}, filepath.Join(repo, "afile"), markers), "not-ignored")
}

func TestProjectBareRepoInside(t *testing.T) {
	_, dir := projectRepo(t)
	write(t, filepath.Join(dir, "mirror", "HEAD"), "ref: x")
	write(t, filepath.Join(dir, "mirror", "objects", "pack", "p"), "x")
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "nested-git")
}
