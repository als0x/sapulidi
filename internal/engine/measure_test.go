package engine

import (
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
)

func TestEngineSetsMeasurementFields(t *testing.T) {
	f := newFix(t)
	p := f.mk("m1")
	c := f.cand(p)
	s := Plan(f.env, []core.Category{f.cat("temp", core.Auto, c)}, Options{})
	if len(s.WouldRemove) != 1 {
		t.Fatalf("%+v", s)
	}
	got := s.WouldRemove[0]
	if got.Bytes <= 0 {
		t.Fatalf("bytes %d", got.Bytes)
	}
	if got.Source != "auto" {
		t.Fatalf("source %q", got.Source)
	}
	if got.Updated.IsZero() || time.Since(got.Updated) < 24*time.Hour {
		t.Fatalf("updated %v must be the old mtime", got.Updated)
	}
	if got.Created.IsZero() {
		t.Skip("filesystem reports no birth time")
	}
}

func TestEngineSourceOptionalOn(t *testing.T) {
	f := newFix(t)
	p := f.mk("opt")
	c := f.cand(p)
	c.Category, c.RuleKey = "deps", "x/deps@1"
	s := Plan(f.env, []core.Category{f.cat("deps", core.Optional, c)}, Options{})
	if len(s.WouldRemove) != 1 || s.WouldRemove[0].Source != "optional-on" {
		t.Fatalf("%+v", s)
	}
}

func TestProgressOncePerCategory(t *testing.T) {
	f := newFix(t)
	type call struct {
		name        string
		done, total int
	}
	var calls []call
	f.env.Progress = func(name string, done, total int) { calls = append(calls, call{name, done, total}) }
	a := f.cat("temp", core.Auto)
	b := f.cat("deps", core.Optional)
	Plan(f.env, []core.Category{a, b}, Options{})
	if len(calls) != 2 {
		t.Fatalf("%+v", calls)
	}
	for i, c := range calls {
		if c.done != i+1 || c.total != 2 {
			t.Fatalf("call %d: %+v", i, c)
		}
	}
	if calls[0].name != "temp" || calls[1].name != "deps" {
		t.Fatalf("%+v", calls)
	}
}

func TestProgressNilIsFine(t *testing.T) {
	f := newFix(t)
	Plan(f.env, []core.Category{f.cat("temp", core.Auto)}, Options{})
}
