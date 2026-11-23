// Package guard holds the pure safety checks the engine and the categories call.
// Each returns nil or a *core.KeepError; none modifies anything.
package guard

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/als0x/sapulidi/internal/core"
)

// Place checks spec S4: path is exactly base/<rel> with no symlink in any component,
// is not a symlink, socket or pipe, and lives on the same device as base.
func Place(p core.Platform, base, path string) error {
	if !filepath.IsAbs(base) || filepath.Clean(base) != base || base == "/" {
		return core.Keep("place", "base is not an absolute, clean, non-root path: "+base)
	}
	bi, err := os.Lstat(base)
	if err != nil {
		return core.Keep("place", "base: "+err.Error())
	}
	if bi.Mode()&os.ModeSymlink != 0 || !bi.IsDir() {
		return core.Keep("place", "base is a symlink or not a directory: "+base)
	}
	if !strings.HasPrefix(path, base+"/") || filepath.Clean(path) != path {
		return core.Keep("place", "path is not lexically below base: "+path)
	}
	rel := path[len(base)+1:]
	if rel == "" {
		return core.Keep("place", "empty relative path")
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." || part == "." || part == "" {
			return core.Keep("place", "bad component in "+rel)
		}
	}
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return core.Keep("place", "base: "+err.Error())
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return core.Keep("place", "path: "+err.Error())
	}
	if realPath != filepath.Join(realBase, rel) {
		return core.Keep("place", "symlink in path: "+path+" resolves to "+realPath)
	}
	pi, err := os.Lstat(path)
	if err != nil {
		return core.Keep("place", "path: "+err.Error())
	}
	if pi.Mode()&(os.ModeSymlink|os.ModeSocket|os.ModeNamedPipe) != 0 {
		return core.Keep("place", "path is a symlink, socket or pipe: "+path)
	}
	same, err := p.SameDevice(base, path)
	if err != nil {
		return core.Keep("place", "device check: "+err.Error())
	}
	if !same {
		return core.Keep("place", "different device than base: "+path)
	}
	return nil
}
