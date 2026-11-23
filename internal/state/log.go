package state

import (
	"encoding/json"
	"os"
	"time"

	"github.com/als0x/sapulidi/internal/core"
)

const logMaxBytes = 1 << 20

type logger struct{ s *Store }

// Logger writes JSON lines to <state>/log.jsonl: one object per event with t, kind and the fields.
// The file is rotated to log.jsonl.1 at 1 MB (one old file is kept). Logging never fails the caller.
func (s *Store) Logger() core.Logger { return logger{s} }

func (l logger) Event(kind string, fields map[string]any) { l.s.log(kind, fields) }

func (s *Store) log(kind string, fields map[string]any) {
	obj := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		obj[k] = v
	}
	obj["t"] = s.now().UTC().Format(time.RFC3339)
	obj["kind"] = kind
	b, err := json.Marshal(obj)
	if err != nil {
		b, _ = json.Marshal(map[string]any{"t": obj["t"], "kind": kind, "error": "unloggable fields: " + err.Error()})
	}
	b = append(b, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeLogLocked(b)
}

func (s *Store) writeLogLocked(b []byte) {
	path := s.path("log.jsonl")
	if fi, err := os.Stat(path); err == nil && fi.Size() >= logMaxBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(b)
	_ = f.Close()
}
