package install

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/als0x/sapulidi/internal/state/atomic"
)

// FileRec is one file written at install time and the SHA-256 of what was written; Path is relative to Entry.Root.
type FileRec struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Entry is what one install added.
type Entry struct {
	Host    string    `json:"host"`
	Root    string    `json:"root"`
	Version string    `json:"version"`
	Time    string    `json:"time"`
	Files   []FileRec `json:"files"`
	// Claude only: which host steps sapulidi itself ran. Removal undoes exactly these.
	AddedMarketplace bool `json:"added_marketplace,omitempty"`
	InstalledPlugin  bool `json:"installed_plugin,omitempty"`
}

// Record is <data>/agents.json: removal touches only what is listed here.
type Record struct {
	Entries []Entry `json:"entries"`
}

func (e *Env) recordPath() string { return filepath.Join(e.Data, "agents.json") }

func (e *Env) load() (Record, error) {
	var r Record
	b, err := os.ReadFile(e.recordPath())
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Record{}, errors.New("agents.json is not valid: " + err.Error())
	}
	return r, nil
}

func (e *Env) save(r Record) error {
	if err := os.MkdirAll(e.Data, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return atomic.WriteFile(e.recordPath(), append(b, '\n'))
}

func (r Record) find(host string) (Entry, bool) {
	for _, en := range r.Entries {
		if en.Host == host {
			return en, true
		}
	}
	return Entry{}, false
}

// put replaces the entry of en.Host, or appends it.
func (r *Record) put(en Entry) {
	for i := range r.Entries {
		if r.Entries[i].Host == en.Host {
			r.Entries[i] = en
			return
		}
	}
	r.Entries = append(r.Entries, en)
}

// drop removes the entry of host.
func (r *Record) drop(host string) {
	out := r.Entries[:0]
	for _, en := range r.Entries {
		if en.Host != host {
			out = append(out, en)
		}
	}
	r.Entries = out
}
