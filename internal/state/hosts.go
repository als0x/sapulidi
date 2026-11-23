package state

import (
	"os"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/state/atomic"
)

// Heartbeat is one host's last sign of life (spec 4.1): the version it pins and when it last ran a hook.
type Heartbeat struct {
	Version string
	At      time.Time
}

func validHost(h string) bool {
	return h != "" && !strings.HasPrefix(h, ".") && !strings.ContainsAny(h, "/\\\x00")
}

// Heartbeat touches <state>/hosts/<host>: the content is the version, the mtime the time. An invalid host name
// is ignored.
func (s *Store) Heartbeat(host, version string) {
	if !validHost(host) {
		return
	}
	p := s.path("hosts", host)
	if err := atomic.WriteFile(p, []byte(version)); err != nil {
		return
	}
	now := s.now()
	_ = os.Chtimes(p, now, now)
}

// Heartbeats returns every host's heartbeat.
func (s *Store) Heartbeats() map[string]Heartbeat {
	out := map[string]Heartbeat{}
	entries, err := os.ReadDir(s.path("hosts"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !validHost(e.Name()) || !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		b, err := os.ReadFile(s.path("hosts", e.Name()))
		if err != nil {
			continue
		}
		out[e.Name()] = Heartbeat{Version: strings.TrimSpace(string(b)), At: fi.ModTime()}
	}
	return out
}
