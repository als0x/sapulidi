package guard

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
)

// nestedClone clones a fresh local bare "remote" (one pushed commit on main) into dir/rel and returns the clone.
func nestedClone(t *testing.T, dir, rel string) string {
	t.Helper()
	remote := filepath.Join(realDir(t), "remote.git")
	git(t, realDir(t), "init", "--bare", "-q", remote)
	seed := filepath.Join(realDir(t), "seed")
	git(t, realDir(t), "init", "-q", seed)
	write(t, filepath.Join(seed, "a.txt"), "a\n")
	git(t, seed, "add", "a.txt")
	nestedCommit(t, seed, "seed")
	git(t, seed, "branch", "-M", "main")
	git(t, seed, "push", "-q", remote, "main")
	clone := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(clone), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "clone", "-q", "-b", "main", remote, clone)
	// A local path as a remote proves nothing, so the clone names a network url; its remote-tracking refs stay.
	git(t, clone, "config", "remote.origin.url", "https://example.invalid/r.git")
	return clone
}

func nestedCommit(t *testing.T, repo, msg string) {
	t.Helper()
	git(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", msg)
}

func wantNested(t *testing.T, err error, reason string) {
	t.Helper()
	wantWhy(t, err, "nested-git")
	if d := err.(*core.KeepError).Detail; !strings.Contains(d, reason) {
		t.Fatalf("detail %q does not name %q", d, reason)
	}
}

func TestNestedCleanPushedCloneIsRemovable(t *testing.T) {
	_, dir := projectRepo(t)
	nestedClone(t, dir, "out/src")
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "")
}

func TestNestedUnsafeClonesAreKept(t *testing.T) {
	cases := []struct {
		name, reason string
		mutate       func(t *testing.T, clone string)
	}{
		{"modified", "dirty", func(t *testing.T, c string) { write(t, filepath.Join(c, "a.txt"), "changed\n") }},
		{"untracked", "untracked", func(t *testing.T, c string) { write(t, filepath.Join(c, "new.txt"), "n\n") }},
		{"stash", "stash", func(t *testing.T, c string) {
			write(t, filepath.Join(c, "a.txt"), "changed\n")
			git(t, c, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "stash", "-q")
		}},
		{"unpushed", "unpushed", func(t *testing.T, c string) {
			write(t, filepath.Join(c, "b.txt"), "b\n")
			git(t, c, "add", "b.txt")
			nestedCommit(t, c, "local")
		}},
		{"detached", "unpushed", func(t *testing.T, c string) {
			git(t, c, "checkout", "-q", "--detach")
			write(t, filepath.Join(c, "b.txt"), "b\n")
			git(t, c, "add", "b.txt")
			nestedCommit(t, c, "local")
		}},
		{"no-remote", "no-remote", func(t *testing.T, c string) { git(t, c, "remote", "remove", "origin") }},
		{"bare", "bare", func(t *testing.T, c string) {
			if err := os.RemoveAll(c); err != nil {
				t.Fatal(err)
			}
			git(t, filepath.Dir(c), "init", "--bare", "-q", c)
		}},
		{"gitfile", "gitfile", func(t *testing.T, c string) {
			if err := os.RemoveAll(filepath.Join(c, ".git")); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(c, ".git"), "gitdir: /nonexistent\n")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, dir := projectRepo(t)
			clone := nestedClone(t, dir, "out/src")
			tc.mutate(t, clone)
			wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), tc.reason)
		})
	}
}

func TestNestedNamesTheFailingRepository(t *testing.T) {
	_, dir := projectRepo(t)
	nestedClone(t, dir, "a/safe")
	dirty := nestedClone(t, dir, "b/dirty")
	write(t, filepath.Join(dirty, "x.txt"), "x\n")
	err := ProjectArtifact(execx.Real{}, dir, markers)
	wantNested(t, err, "dirty")
	if d := err.(*core.KeepError).Detail; !strings.Contains(d, filepath.Join("b", "dirty")) || strings.Contains(d, "safe") {
		t.Fatalf("detail %q should name only b/dirty", d)
	}
}

func TestNestedMoreThanSixteenIsKept(t *testing.T) {
	_, dir := projectRepo(t)
	for i := range 17 {
		write(t, filepath.Join(dir, fmt.Sprintf("r%02d", i), ".git", "HEAD"), "ref: x")
	}
	wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), "more than 16 nested repositories")
}

func TestNestedProofRunsNoFsmonitor(t *testing.T) {
	_, dir := projectRepo(t)
	clone := nestedClone(t, dir, "out/src")
	marker := trapFsmonitor(t, clone, clone)
	wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), "config")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the nested proof ran the nested repository's core.fsmonitor command")
	}
}

func TestNestedGainingWorkBetweenScanAndProofKeeps(t *testing.T) {
	_, dir := projectRepo(t)
	clone := nestedClone(t, dir, "out/src")
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "")
	write(t, filepath.Join(clone, "a.txt"), "edited after the scan\n")
	wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), "dirty")
}

func TestNestedKeepsWhatOnlyFilesRevealAndWhatGitMisses(t *testing.T) {
	cases := []struct {
		name, reason string
		mutate       func(t *testing.T, clone string)
	}{
		{"tag-only commit", "unpushed", func(t *testing.T, c string) {
			write(t, filepath.Join(c, "b.txt"), "b\n")
			git(t, c, "add", "b.txt")
			nestedCommit(t, c, "tagged")
			git(t, c, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "tag", "-a", "v1", "-m", "x")
			git(t, c, "reset", "-q", "--hard", "origin/main")
		}},
		{"linked worktree outside", "worktrees", func(t *testing.T, c string) {
			git(t, c, "worktree", "add", "-q", "-b", "feat", filepath.Join(realDir(t), "wt"))
		}},
		{"file url remote", "local-remote", func(t *testing.T, c string) {
			git(t, c, "config", "remote.origin.url", "file://"+filepath.Join(realDir(t), "r.git"))
		}},
		{"relative remote", "local-remote", func(t *testing.T, c string) {
			git(t, c, "config", "remote.origin.url", "../remote.git")
		}},
		{"absolute remote", "local-remote", func(t *testing.T, c string) {
			git(t, c, "config", "remote.origin.url", "/srv/remote.git")
		}},
		{"core.worktree", "config", func(t *testing.T, c string) { git(t, c, "config", "core.worktree", c) }},
		{"alternates", "alternates", func(t *testing.T, c string) {
			write(t, filepath.Join(c, ".git", "objects", "info", "alternates"), "/elsewhere/objects\n")
		}},
		{"replace ref", "replace", func(t *testing.T, c string) {
			write(t, filepath.Join(c, ".git", "refs", "replace", strings.Repeat("a", 40)), strings.Repeat("b", 40)+"\n")
		}},
		{"grafts", "replace", func(t *testing.T, c string) { write(t, filepath.Join(c, ".git", "info", "grafts"), "") }},
		{"empty .git directory", "identity", func(t *testing.T, c string) {
			if err := os.RemoveAll(c); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(c, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{".git with objects but no HEAD", "identity", func(t *testing.T, c string) {
			if err := os.RemoveAll(c); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(c, ".git", "objects"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, dir := projectRepo(t)
			clone := nestedClone(t, dir, "out/src")
			tc.mutate(t, clone)
			wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), tc.reason)
		})
	}
}

func TestNestedFilterDriverNeverRuns(t *testing.T) {
	_, dir := projectRepo(t)
	clone := nestedClone(t, dir, "out/src")
	marker := filepath.Join(realDir(t), "filter-ran")
	write(t, filepath.Join(clone, ".git", "info", "attributes"), "* filter=trap\n")
	git(t, clone, "config", "filter.trap.clean", "touch '"+marker+"'; cat")
	write(t, filepath.Join(clone, "a.txt"), "changed\n")
	wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), "config")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a filter driver from the nested repository's config ran")
	}
}

func TestNestedIgnoredSecretStillKeepsViaSecretGuard(t *testing.T) {
	_, dir := projectRepo(t)
	clone := nestedClone(t, dir, "out/src")
	write(t, filepath.Join(clone, ".git", "info", "exclude"), ".env\n")
	write(t, filepath.Join(clone, ".env"), "TOKEN=x\n")
	wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "")
	wantWhy(t, Secret(dir), "secret")
}

func TestScanConfig(t *testing.T) {
	plain := "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n[remote \"origin\"]\n\turl = https://h/r.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n"
	cases := []struct{ name, body, reason string }{
		{"plain clone", plain, ""},
		{"filter section", plain + "[filter \"x\"]\n\tclean = c\n", "config"},
		{"filter upper case", plain + "[FILTER \"x\"]\n\tCLEAN = c\n", "config"},
		{"filter dotted", plain + "[filter.x]\n\tclean = c\n", "config"},
		{"quoted section name", plain + "[\"filter\" \"x\"]\n\tclean = c\n", "config"},
		{"include", plain + "[include]\n\tpath = /x\n", "config"},
		{"includeIf", plain + "[includeIf \"gitdir:/\"]\n\tpath = /x\n", "config"},
		{"attributesFile", plain + "[core]\n\tattributesFile = /x\n", "config"},
		{"fsmonitor", plain + "[core]\n\tfsmonitor = /x\n", "config"},
		{"hooksPath", plain + "[core]\n\tHOOKSPATH = /x\n", "config"},
		{"sshCommand", plain + "[core]\n\tsshCommand = x\n", "config"},
		{"gitProxy", plain + "[core]\n\tgitProxy = x\n", "config"},
		{"packObjectsHook", plain + "[uploadpack]\n\tpackObjectsHook = x\n", "config"},
		{"key on the header line", "[core] fsmonitor = x\n", "config"},
		{"continuation hides a key", "[core]\n\tfilemode = true \\\n\tfsmonitor = x\n[core]\n\tfsmonitor = y\n", "config"},
		{"unterminated header", "[core\n", "config"},
		{"key before any section", "filemode = true\n", "config"},
		{"url with a quote", "[remote \"o\"]\n\turl = \"x\"\n", "config"},
		{"bare", "[core]\n\tbare = true\n", "bare"},
		{"no config file at all", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(realDir(t), "config")
			write(t, p, tc.body)
			if _, got := scanConfig(p); got != tc.reason {
				t.Fatalf("reason = %q, want %q", got, tc.reason)
			}
		})
	}
	if _, got := scanConfig(filepath.Join(realDir(t), "missing")); got != "" {
		t.Fatalf("missing file: %q", got)
	}
	big := filepath.Join(realDir(t), "big")
	write(t, big, "# "+strings.Repeat("x", maxRead)+"\n")
	if _, got := scanConfig(big); got != "config" {
		t.Fatalf("oversized config: %q", got)
	}
}

func TestNetworkURL(t *testing.T) {
	for u, want := range map[string]bool{
		"https://h/r.git": true, "ssh://git@h/r.git": true, "git@github.com:o/r.git": true, "git://h/r": true,
		"file:///x": false, "FILE:///x": false, "file:/x": false, "/abs/r.git": false, "../r.git": false,
		"r.git": false, "ext::sh -c x": false, "": false, "ftp://h/r": false,
	} {
		if got := networkURL(u); got != want {
			t.Errorf("networkURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestNestedOtherFilesystemIsKept(t *testing.T) {
	_, dir := projectRepo(t)
	nestedClone(t, dir, "out/src")
	write(t, filepath.Join(dir, "mnt", "f"), "x")
	orig := statDev
	t.Cleanup(func() { statDev = orig })
	statDev = func(fi fs.FileInfo) (uint64, bool) {
		dev, ok := orig(fi)
		if fi.Name() == "mnt" {
			dev++
		}
		return dev, ok
	}
	wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), "other-filesystem")
}

// blockRunner answers only when its context ends; out, when set, is returned at once instead.
type blockRunner struct{ out []byte }

func (r blockRunner) Run(ctx context.Context, dir string, argv ...string) ([]byte, error) {
	if r.out != nil {
		return r.out, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockRunner) LookPath(string) (string, error) { return "", os.ErrNotExist }

func TestNestedBudgetBoundsTheWholeProof(t *testing.T) {
	_, dir := projectRepo(t)
	nestedClone(t, dir, "out/src")
	orig := NestedBudget
	t.Cleanup(func() { NestedBudget = orig })
	NestedBudget = 100 * time.Millisecond
	start := time.Now()
	err := nestedProof(blockRunner{}, dir)
	if took := time.Since(start); took > time.Second {
		t.Fatalf("the proof took %v with a 100ms budget", took)
	}
	wantWhy(t, err, "nested-git")
}

func TestScanConfigRefusesWhatGitReadsDifferently(t *testing.T) {
	plain := "[core]\n\trepositoryformatversion = 0\n"
	url := "[remote \"origin\"]\n\turl = https://h/r.git\n"
	cases := []struct{ name, body, reason string }{
		{"filter hidden behind comment continuations",
			plain + "# a \\\n[filter \"x\"]\n# b \\\n\tclean = /tmp/evil.sh\n" + url, "config"},
		{"filter behind a value comment continuation",
			plain + "\tfilemode = true # x \\\n[filter \"x\"]\n\tclean = /tmp/evil.sh\n" + url, "config"},
		{"lone carriage return", plain + "\r[filter \"x\"]\r\tclean = c\r" + url, "config"},
		{"NUL", plain + "\x00" + url, "config"},
		{"CRLF line ends are fine", strings.ReplaceAll(plain+url, "\n", "\r\n"), ""},
		{"bare = false", plain + "\tbare = false\n", ""},
		{"bare off", plain + "\tbare = OFF\n", ""},
		{"bare without a value", plain + "\tbare\n", "bare"},
		{"bare empty", plain + "\tbare =\n", "bare"},
		{"bare unknown", plain + "\tbare = maybe\n", "bare"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(realDir(t), "config")
			write(t, p, tc.body)
			if _, got := scanConfig(p); got != tc.reason {
				t.Fatalf("reason = %q, want %q", got, tc.reason)
			}
		})
	}
}

func TestNestedHiddenFilterNeverRuns(t *testing.T) {
	_, dir := projectRepo(t)
	clone := nestedClone(t, dir, "out/src")
	marker := filepath.Join(realDir(t), "filter-ran")
	write(t, filepath.Join(clone, ".git", "info", "attributes"), "* filter=x\n")
	write(t, filepath.Join(clone, ".git", "config"), "[core]\n\trepositoryformatversion = 0\n# a \\\n[filter \"x\"]\n# b \\\n"+
		"\tclean = touch '"+marker+"'; cat\n[remote \"origin\"]\n\turl = https://h/r.git\n")
	write(t, filepath.Join(clone, "a.txt"), "changed\n")
	wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), "config")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a filter hidden behind a comment continuation ran")
	}
}

func TestNestedOddlySpelledGitDirectories(t *testing.T) {
	t.Run("upper case .GIT with unpushed work", func(t *testing.T) {
		_, dir := projectRepo(t)
		clone := nestedClone(t, dir, "out/src")
		write(t, filepath.Join(clone, "b.txt"), "b\n")
		git(t, clone, "add", "b.txt")
		nestedCommit(t, clone, "local")
		if err := os.Rename(filepath.Join(clone, ".git"), filepath.Join(clone, ".GIT")); err != nil {
			t.Fatal(err)
		}
		wantWhy(t, ProjectArtifact(execx.Real{}, dir, markers), "nested-git")
	})
	t.Run("zero-width characters inside the name", func(t *testing.T) {
		_, dir := projectRepo(t)
		write(t, filepath.Join(dir, "out", "src", ".g‌it", "x"), "x")
		wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), "gitname")
	})
}

func TestNestedRedirectingGitDirEntries(t *testing.T) {
	for _, name := range []string{"commondir", "gitdir", "config.worktree", filepath.Join("objects", "info", "http-alternates")} {
		t.Run(name, func(t *testing.T) {
			_, dir := projectRepo(t)
			clone := nestedClone(t, dir, "out/src")
			write(t, filepath.Join(clone, ".git", name), "/elsewhere\n")
			reason := "redirect"
			if strings.Contains(name, "alternates") {
				reason = "alternates"
			}
			wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), reason)
		})
	}
}

func TestNestedHandMadeRemoteRefDoesNotCount(t *testing.T) {
	_, dir := projectRepo(t)
	clone := nestedClone(t, dir, "out/src")
	write(t, filepath.Join(clone, "b.txt"), "b\n")
	git(t, clone, "add", "b.txt")
	nestedCommit(t, clone, "local")
	git(t, clone, "update-ref", "refs/remotes/mirror/main", "HEAD")
	wantNested(t, ProjectArtifact(execx.Real{}, dir, markers), "unpushed")
}

func TestNetworkURLRefusesLoopback(t *testing.T) {
	for u, want := range map[string]bool{
		"https://localhost/r.git": false, "ssh://git@127.0.0.1/r": false, "http://[::1]:8080/r": false,
		"git@localhost:o/r.git": false, "https://0.0.0.0/r": false, "https://user@github.com:443/r": true,
		"git@127.0.0.2:r": false, "https:///r": false,
	} {
		if got := networkURL(u); got != want {
			t.Errorf("networkURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestNestedGitOutputIsCapped(t *testing.T) {
	_, dir := projectRepo(t)
	nestedClone(t, dir, "out/src")
	wantNested(t, nestedProof(blockRunner{out: make([]byte, maxRead+1)}, dir), "error")
}
