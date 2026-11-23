package guard

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/als0x/sapulidi/internal/core"
)

var secretPatterns = []string{".env", ".env.*", "*.pem", "*.key", "*.p12", "*.pfx", "id_rsa*", "id_ed25519*",
	"id_ecdsa*", "*.kdbx", "credentials*", "*.tfstate", "*.tfstate.*"}

func isSecretName(name string) bool {
	name = strings.ToLower(name)
	for _, pat := range secretPatterns {
		if ok, _ := filepath.Match(pat, name); ok {
			return true
		}
	}
	return false
}

// packageDir reports whether a directory's content comes from a package registry.
func packageDir(path string) bool {
	name := filepath.Base(path)
	parent := filepath.Base(filepath.Dir(path))
	switch {
	case name == "site-packages", name == "dist-packages":
		return true
	case parent == ".cargo" && (name == "registry" || name == "git"):
		return true
	// Reading: every package directory directly inside node_modules is skipped whole, so
	// node_modules/pkg/.env is not reported by design; node_modules/.env (above the package
	// level) still is.
	case parent == "node_modules":
		return true
	case parent == "vendor" && name == "bundle":
		return true
	case parent == "pkg" && name == "mod":
		return true
	}
	return false
}

var errFound = errors.New("found")

// Secret keeps a candidate that holds a credential or key file anywhere inside it
// (spec 3.1 "never"), without following symlinks.
func Secret(path string) error {
	if isSecretName(filepath.Base(path)) {
		return core.Keep("secret", filepath.Base(path))
	}
	var found string
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == path {
			return nil
		}
		if isSecretName(d.Name()) {
			found, _ = filepath.Rel(path, p)
			return errFound
		}
		if d.IsDir() && packageDir(p) {
			return filepath.SkipDir
		}
		return nil
	})
	switch {
	case err == errFound:
		return core.Keep("secret", found)
	case err != nil:
		return core.Keep("error", err.Error())
	}
	return nil
}
