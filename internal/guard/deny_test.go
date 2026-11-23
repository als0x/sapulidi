package guard

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/als0x/sapulidi/internal/testutil"
)

func home(t *testing.T) string {
	t.Helper()
	h, err := filepath.EvalSymlinks(testutil.TempHome(t))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestDenyHomeAndChildren(t *testing.T) {
	h := home(t)
	for _, p := range []string{h, "Documents", "Downloads", "Desktop", "Pictures", "Music", "Movies", "Library", "Code", "stuff",
		"Library/Application Support", "Library/Application Support/Foo", "Library/Preferences/x.plist",
		"Library/Keychains", "Library/Mail", "Library/Messages", "Library/Mobile Documents/x"} {
		if p != h {
			p = filepath.Join(h, p)
		}
		wantWhy(t, Deny(h, "", p), "deny")
	}
	for _, p := range []string{"/", "/System/x", "/usr/local", "/etc/hosts", "/var/lib", "/Applications", "/opt/x", "/root"} {
		wantWhy(t, Deny(h, "", p), "deny")
	}
	// Below a non-dot child is a normal project location.
	wantWhy(t, Deny(h, "", filepath.Join(h, "Code", "proj", "node_modules")), "")
	wantWhy(t, Deny(h, "", filepath.Join(h, ".cache", "go-build")), "")
}

func TestDenyCaseInsensitive(t *testing.T) {
	h := home(t)
	wantWhy(t, Deny(h, "", filepath.Join(h, "DOCUMENTS")), "deny")
	wantWhy(t, Deny(h, "", filepath.Join(h, "library", "application support", "x")), "deny")
	wantWhy(t, Deny(h, "", filepath.Join(h, "Code", "p", ".GIT")), "deny")
	wantWhy(t, Deny(h, "", filepath.Join(h, "Code", "p", "claude.md")), "deny")
	wantWhy(t, Deny(h, "", filepath.Join(h, ".SSH", "id")), "deny")
}

func TestDenyAllowsLibraryCaches(t *testing.T) {
	h := home(t)
	for _, p := range []string{"Library/Caches/foo", "Library/Developer/Xcode/DerivedData/App-abc",
		"Library/Developer/Xcode/iOS DeviceSupport/17.2 (21C66)", "Library/Developer/CoreSimulator/Devices/x", "Library/Application Support/Claude/vm_bundles/x"} {
		wantWhy(t, Deny(h, "", filepath.Join(h, p)), "")
	}
	wantWhy(t, Deny(h, "", filepath.Join(h, "Library", "Developer", "Xcode", "iOS DeviceSupport")), "deny")
	wantWhy(t, Deny(h, "", filepath.Join(h, "Library", "Developer", "Xcode", "Archives", "x")), "deny")
	wantWhy(t, Deny(h, "", filepath.Join(h, "Library", "Caches")), "deny")
	wantWhy(t, Deny(h, "", filepath.Join(h, "Library", "Application Support", "Claude")), "deny")
}

func TestDenyInsideGit(t *testing.T) {
	h := home(t)
	wantWhy(t, Deny(h, "", filepath.Join(h, "Code", "p", ".git")), "deny")
	wantWhy(t, Deny(h, "", filepath.Join(h, "Code", "p", ".git", "objects", "ab")), "deny")
}

func TestDenyAgentMemoryNames(t *testing.T) {
	h := home(t)
	for _, p := range []string{"Code/p/CLAUDE.md", "Code/p/AGENTS.md", "Code/p/GEMINI.md", ".claude/settings.json",
		".claude/settings.local.json", ".claude/projects/x/memory", ".codex/auth.json", ".x/credentials.json",
		".x/.credentials.json", ".ssh", ".ssh/id_rsa", ".gnupg/x", ".aws/credentials", ".kube/config", ".config/gh/hosts.yml"} {
		wantWhy(t, Deny(h, "", filepath.Join(h, p)), "deny")
	}
}

func TestDenyContainingDeniedPath(t *testing.T) {
	h := home(t)
	wantWhy(t, Deny(h, "", filepath.Join(h, ".config")), "deny")
	if runtime.GOOS == "darwin" {
		wantWhy(t, Deny(h, "", filepath.Dir(h)), "deny")
	} else {
		// A Linux test home lives under /tmp, an allowed base, so its parent is not outside home and temp.
		wantWhy(t, Deny(h, "", "/"), "deny")
		wantWhy(t, Deny(h, "", "/usr"), "deny")
	}
	wantWhy(t, Deny(h, "", filepath.Join(h, ".config", "other")), "")
}

func TestDenyTempDirItselfWhenUnderTmp(t *testing.T) {
	// TMPDIR=/tmp/custom: the temp directory itself is denied even though it is below /tmp.
	h := home(t)
	wantWhy(t, Deny(h, "/tmp/sapulidi-custom", "/tmp/sapulidi-custom"), "deny")
	wantWhy(t, Deny(h, "/tmp/sapulidi-custom", "/tmp/sapulidi-custom/build-1"), "")
}

func TestDenyAllowsTempDirUnderPrivateVar(t *testing.T) {
	tmp := t.TempDir() // on macOS /var/folders/..., really /private/var/folders/...
	h := home(t)
	wantWhy(t, Deny(h, tmp, filepath.Join(tmp, "build-1")), "")
	if runtime.GOOS == "darwin" {
		wantWhy(t, Deny(h, tmp, filepath.Join("/private", tmp, "build-1")), "")
	}
	wantWhy(t, Deny(h, tmp, tmp), "deny")
	if runtime.GOOS == "darwin" {
		// The macOS temp dir is under /var/folders, outside /tmp: its parent and a child are denied without tempDir.
		// On Linux t.TempDir() is under /tmp, which is always an allowed base, so these two are allowed there.
		wantWhy(t, Deny(h, tmp, filepath.Dir(tmp)), "deny")
		wantWhy(t, Deny(h, "", filepath.Join(tmp, "build-1")), "deny")
	} else {
		wantWhy(t, Deny(h, "", filepath.Join(tmp, "build-1")), "")
	}
	wantWhy(t, Deny(h, "", "/tmp/agent-scratch"), "")
	wantWhy(t, Deny(h, "", "/private/tmp/agent-scratch"), "")
	wantWhy(t, Deny(h, "", "/tmp"), "deny")
}

func TestDenyHomeInsideSystemTemp(t *testing.T) {
	h := home(t) // TempHome lives under the system temp dir
	wantWhy(t, Deny(h, "", filepath.Join(h, "Code", "p", "node_modules")), "")
	wantWhy(t, Deny(h, "", filepath.Join(h, "Documents")), "deny")
	wantWhy(t, Deny(h, "", filepath.Join(h, "Code")), "deny")
	wantWhy(t, DenyRoot(h, filepath.Join(h, "Code")), "")
}

func TestDenyRootAllowsHomeChild(t *testing.T) {
	h := home(t)
	wantWhy(t, DenyRoot(h, filepath.Join(h, "Code")), "")
	wantWhy(t, DenyRoot(h, h), "deny")
	wantWhy(t, DenyRoot(h, "/"), "deny")
	wantWhy(t, DenyRoot(h, filepath.Dir(h)), "deny")
	wantWhy(t, DenyRoot(h, "/usr/local"), "deny")
	wantWhy(t, DenyRoot(h, "/etc"), "deny")
	wantWhy(t, DenyRoot(h, filepath.Join(h, "Library", "Mail")), "deny")
	wantWhy(t, DenyRoot(h, filepath.Join(h, ".ssh")), "deny")
	// a symlink root resolves to its target
	link := filepath.Join(h, "lnk")
	if err := os.Symlink(h, link); err != nil {
		t.Fatal(err)
	}
	wantWhy(t, DenyRoot(h, link), "deny")
}

func TestDenyEverywhereRulesInsideAllowedLibrary(t *testing.T) {
	h := home(t)
	tmp := t.TempDir()
	for _, p := range []string{"Library/Caches/x/.git/objects", "Library/Caches/foo/CLAUDE.md", "Library/Developer/CoreSimulator/a/memory"} {
		wantWhy(t, Deny(h, tmp, filepath.Join(h, p)), "deny")
	}
	wantWhy(t, Deny(h, tmp, filepath.Join(tmp, "x", ".git", "o")), "deny")
}

func TestDenyLibraryOutsideAllowList(t *testing.T) {
	h := home(t)
	for _, p := range []string{"Library/Containers/com.x/Data", "Library/Group Containers", "Library/Cookies", "Library/Safari",
		"Library/Logs", "Library/Developer", "Library/Developer/Xcode/Archives"} {
		wantWhy(t, Deny(h, "", filepath.Join(h, p)), "deny")
	}
	wantWhy(t, Deny(h, "", filepath.Join(h, "Library/Caches/foo")), "")
	wantWhy(t, DenyRoot(h, filepath.Join(h, "Library/Logs")), "deny")
}
