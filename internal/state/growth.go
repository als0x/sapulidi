package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"time"

	"github.com/als0x/sapulidi/internal/state/atomic"
)

const growthKeep = 5 * 7 * 24 * time.Hour

type sample struct {
	T     time.Time        `json:"t"`
	Sizes map[string]int64 `json:"sizes"`
}

func (s *Store) readSamples() []sample {
	f, err := os.Open(s.path("growth.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []sample
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		var sm sample
		if json.Unmarshal(sc.Bytes(), &sm) == nil {
			out = append(out, sm)
		}
	}
	return out
}

// AddGrowthSample appends one line with the sizes of the top directories and drops samples older than 5 weeks.
func (s *Store) AddGrowthSample(sizes map[string]int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	var buf bytes.Buffer
	for _, sm := range append(s.readSamples(), sample{T: now, Sizes: sizes}) {
		if now.Sub(sm.T) > growthKeep {
			continue
		}
		b, err := json.Marshal(sm)
		if err != nil {
			continue
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	_ = atomic.WriteFile(s.path("growth.jsonl"), buf.Bytes())
}

// Growth is the size of dir in the newest sample minus its size in the oldest sample inside the window;
// 0 when fewer than two samples in the window know the directory.
func (s *Store) Growth(dir string, window time.Duration) int64 {
	s.mu.Lock()
	samples := s.readSamples()
	s.mu.Unlock()
	cutoff := s.now().Add(-window)
	var first, last *int64
	for _, sm := range samples { // appended in time order
		if sm.T.Before(cutoff) {
			continue
		}
		v, ok := sm.Sizes[dir]
		if !ok {
			continue
		}
		v2 := v
		if first == nil {
			first = &v2
		}
		last = &v2
	}
	if first == nil || last == first {
		return 0
	}
	return *last - *first
}
