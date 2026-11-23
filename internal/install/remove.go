package install

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// allowedRoot is true when root is <data>/plugin, or a folder called sapulidi directly inside a host's skills folder.
func allowedRoot(data, home, root string) bool {
	if root == filepath.Join(data, "plugin") {
		return true
	}
	if filepath.Base(root) != "sapulidi" {
		return false
	}
	for _, h := range Hosts {
		if filepath.Dir(root) == filepath.Join(home, filepath.FromSlash(h.Skills)) {
			return true
		}
	}
	return false
}

// removeRecorded is the ONLY place in this package that deletes. It removes the files of one recorded entry
// and nothing else: the root must be an allowed one; no symlink is followed or removed; a file goes only when
// its SHA-256 still equals the recorded one; folders go only when empty, one by one (never a recursive
// delete). It returns how many files it removed and one line per thing it kept or refused.
func removeRecorded(data, home string, e Entry) (int, []string) {
	root := filepath.Clean(e.Root)
	if !allowedRoot(data, home, root) {
		return 0, []string{fmt.Sprintf("refused: %s is not a folder sapulidi installs into", e.Root)}
	}
	ri, err := os.Lstat(root)
	if err != nil {
		return 0, nil // already gone
	}
	if ri.Mode()&os.ModeSymlink != 0 || !ri.IsDir() {
		return 0, []string{fmt.Sprintf("refused: %s is a symlink or not a folder, kept", root)}
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return 0, []string{fmt.Sprintf("refused: cannot resolve %s: %v", root, err)}
	}
	removed := 0
	var notes []string
	dirs := map[string]bool{root: true}
	for _, f := range e.Files {
		rel := filepath.FromSlash(f.Path)
		if !filepath.IsLocal(rel) {
			notes = append(notes, fmt.Sprintf("refused: recorded path %q leaves the folder", f.Path))
			continue
		}
		p := filepath.Join(root, rel)
		fi, err := os.Lstat(p)
		if err != nil {
			continue // already gone
		}
		realParent, err := filepath.EvalSymlinks(filepath.Dir(p))
		if err != nil || !within(realRoot, realParent) {
			notes = append(notes, fmt.Sprintf("refused: %s resolves outside the folder, kept", p))
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			notes = append(notes, fmt.Sprintf("refused: %s is a symlink, kept", p))
			continue
		}
		if !fi.Mode().IsRegular() {
			notes = append(notes, fmt.Sprintf("refused: %s is not a regular file, kept", p))
			continue
		}
		sum, err := hashFile(p)
		if err != nil || sum != f.SHA256 {
			notes = append(notes, fmt.Sprintf("%s: changed since install, kept", p))
			continue
		}
		if err := os.Remove(p); err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		removed++
		for d := filepath.Dir(p); within(root, d) && d != root; d = filepath.Dir(d) {
			dirs[d] = true
		}
	}
	list := make([]string, 0, len(dirs))
	for d := range dirs {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	for _, d := range list {
		di, err := os.Lstat(d)
		if err != nil || di.Mode()&os.ModeSymlink != 0 || !di.IsDir() {
			continue
		}
		if err := os.Remove(d); err != nil && !os.IsNotExist(err) {
			notes = append(notes, fmt.Sprintf("%s: not empty, kept", d))
		}
	}
	return removed, notes
}
