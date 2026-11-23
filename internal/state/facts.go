package state

import (
	"encoding/json"
	"os"
	"sort"
	"time"
)

type fact struct {
	Text        string `json:"text"`
	Undelivered bool   `json:"undelivered,omitempty"`
}

func (s *Store) loadFacts() map[string]fact {
	m := map[string]fact{}
	ok, err := s.readJSON("facts.json", &m)
	if err != nil {
		// Keep the unreadable file for inspection before it is overwritten. The caller holds s.mu.
		_ = os.Rename(s.path("facts.json"), s.path("facts.json.bad"))
		b, _ := json.Marshal(map[string]any{"t": s.now().UTC().Format(time.RFC3339), "kind": "facts-corrupt", "error": err.Error()})
		s.writeLogLocked(append(b, '\n'))
		return map[string]fact{}
	}
	if !ok {
		return map[string]fact{}
	}
	return m
}

// Fact records a fact and says whether to notify the user: true when the key is new, its text differs from the
// stored one, or the caller says it changed (a fact with identical text that the caller knows is new again).
// A new or changed fact is logged; a repeated one is neither logged nor notified.
func (s *Store) Fact(key, text string, changed bool) (notify bool) {
	s.mu.Lock()
	m := s.loadFacts()
	old, had := m[key]
	notify = !had || old.Text != text || changed
	if notify {
		m[key] = fact{Text: text}
		_ = s.writeJSON("facts.json", m)
	}
	s.mu.Unlock()
	if notify {
		s.log("fact", map[string]any{"key": key, "text": text})
	}
	return notify
}

// MarkUndelivered flags a fact whose notification failed, so `check` can show it.
func (s *Store) MarkUndelivered(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.loadFacts()
	f, ok := m[key]
	if !ok {
		return
	}
	f.Undelivered = true
	m[key] = f
	_ = s.writeJSON("facts.json", m)
}

// PendingFacts returns the texts of the flagged facts, ordered by key, and clears the flags.
func (s *Store) PendingFacts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.loadFacts()
	var keys []string
	for k, f := range m {
		if f.Undelivered {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		f := m[k]
		out = append(out, f.Text)
		f.Undelivered = false
		m[k] = f
	}
	_ = s.writeJSON("facts.json", m)
	return out
}
