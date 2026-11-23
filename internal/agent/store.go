package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxProposalFile = 1 << 20

func file(dir, id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("proposal id %q is not valid", id)
	}
	return filepath.Join(dir, id+".json"), nil
}

// Save writes one new proposal file. It never overwrites: an existing id is an error.
func Save(dir string, p Proposal) error {
	path, err := file(dir, p.ID)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("proposal %q: %w", p.ID, err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

// Load reads one proposal file. The content is data and is validated again before it is applied.
func Load(dir, id string) (Proposal, error) {
	path, err := file(dir, id)
	if err != nil {
		return Proposal{}, err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Proposal{}, fmt.Errorf("no proposal %q", id)
		}
		return Proposal{}, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxProposalFile {
		return Proposal{}, fmt.Errorf("proposal %q is not a regular file of a sane size", id)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Proposal{}, err
	}
	var p Proposal
	if err := json.Unmarshal(b, &p); err != nil {
		return Proposal{}, fmt.Errorf("proposal %q: %w", id, err)
	}
	if p.ID != id {
		return Proposal{}, fmt.Errorf("proposal %q: the file names id %q", id, p.ID)
	}
	return p, nil
}

// List returns the readable proposals, ordered by id. A damaged file is skipped.
func List(dir string) ([]Proposal, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Proposal
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !ValidID(id) {
			continue
		}
		if p, err := Load(dir, id); err == nil {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Remove deletes one proposal file (sapulidi's own file, never user data).
func Remove(dir, id string) error {
	path, err := file(dir, id)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err != nil {
		return fmt.Errorf("no proposal %q", id)
	}
	return os.Remove(path)
}
