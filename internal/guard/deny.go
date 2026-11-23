package guard

import (
	"path/filepath"
	"strings"

	"github.com/als0x/sapulidi/internal/core"
)

// realPath resolves symlinks in the existing part of p and appends the rest unchanged.
func realPath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

func lowerReal(p string) string { return strings.ToLower(realPath(p)) }

// under reports whether p is base or below it ("/" is below nothing but itself).
func under(base, p string) bool {
	if p == base {
		return true
	}
	if base == "/" {
		return true
	}
	return strings.HasPrefix(p, base+"/")
}

func strictlyUnder(base, p string) bool { return p != base && under(base, p) }

var deniedNames = map[string]bool{
	"claude.md": true, "agents.md": true, "gemini.md": true, "settings.json": true, "settings.local.json": true,
	"memory": true, "auth.json": true, "credentials.json": true, ".credentials.json": true,
}

// denyNames applies the rules that hold everywhere to the part of a path below its anchor.
func denyNames(rel string) error {
	for _, part := range strings.Split(rel, "/") {
		if part == ".git" {
			return core.Keep("deny", "inside a .git directory")
		}
		if deniedNames[part] {
			return core.Keep("deny", "agent memory or config name: "+part)
		}
	}
	return nil
}

var systemDirs = []string{"/system", "/usr", "/bin", "/sbin", "/etc", "/private/etc", "/var", "/private/var",
	"/library", "/applications", "/mnt", "/media", "/volumes", "/opt", "/srv", "/root", "/proc", "/sys", "/dev", "/boot", "/lib"}

// homeDenied lists the directories (lowercase, relative to home) denied with everything below them.
// Everything under home/Library is handled separately: denied unless below homeLibraryAllowed.
var homeDenied = []string{".ssh", ".gnupg", ".aws", ".kube", ".config/gh"}

// homeLibraryAllowed are the only places below Library that stay candidates (their own root is not).
var homeLibraryAllowed = []string{"library/caches", "library/developer/xcode/deriveddata", "library/developer/xcode/ios devicesupport", "library/developer/coresimulator",
	"library/application support/claude/vm_bundles"}

// denyHomeDirs applies the fixed home directories (rh and rp lowercase real paths).
func denyHomeDirs(rh, rp string) error {
	if under(rh+"/library", rp) {
		for _, a := range homeLibraryAllowed {
			abs := rh + "/" + a
			if strictlyUnder(abs, rp) {
				return denyNames(rp[len(abs)+1:])
			}
		}
		return core.Keep("deny", "home/Library outside the cache locations")
	}
	for _, d := range homeDenied {
		if under(rh+"/"+d, rp) {
			return core.Keep("deny", "protected home directory: "+d)
		}
		if strictlyUnder(rp, rh+"/"+d) {
			return core.Keep("deny", "contains protected home directory: "+d)
		}
	}
	return nil
}

// Deny is spec S3 for candidates: it refuses what must never be removed.
func Deny(home, tempDir, path string) error {
	rp := lowerReal(path)
	rh := lowerReal(home)
	if home != "" && rh != "/" && under(rh, rp) {
		if rp == rh {
			return core.Keep("deny", "home itself")
		}
		rel := rp[len(rh)+1:]
		if !strings.Contains(rel, "/") && !strings.HasPrefix(rel, ".") {
			return core.Keep("deny", "top-level home folder: "+rel)
		}
		if err := denyHomeDirs(rh, rp); err != nil {
			return err
		}
		return denyNames(rel)
	}
	bases := []string{"/tmp", "/private/tmp"}
	if tempDir != "" {
		bases = append(bases, lowerReal(tempDir))
	}
	for _, b := range bases {
		if rp == b {
			return core.Keep("deny", "temp directory itself")
		}
	}
	for _, b := range bases {
		if strictlyUnder(b, rp) {
			return denyNames(rp[len(b)+1:])
		}
	}
	return core.Keep("deny", "outside home and temp: "+rp)
}

// DenyRoot refuses a discovery root that is "/", home itself, an ancestor of home, a system location
// or a protected home directory. A root such as ~/Code is fine.
func DenyRoot(home, path string) error {
	rp := lowerReal(path)
	rh := lowerReal(home)
	if rp == "/" || rp == rh {
		return core.Keep("deny", "root is / or home")
	}
	if home != "" && under(rp, rh) {
		return core.Keep("deny", "root contains home")
	}
	if home != "" && rh != "/" && under(rh, rp) {
		if err := denyHomeDirs(rh, rp); err != nil {
			return err
		}
		return nil
	}
	for _, s := range systemDirs {
		if under(s, rp) {
			return core.Keep("deny", "system location: "+s)
		}
	}
	return nil
}
