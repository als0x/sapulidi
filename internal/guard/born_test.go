package guard

import (
	"testing"
	"time"
)

func TestBornOfFreshDir(t *testing.T) {
	dir := t.TempDir()
	b := bornOf(dir)
	if b.IsZero() {
		t.Skip("this filesystem reports no birth time")
	}
	if d := time.Since(b); d < -5*time.Second || d > 10*time.Second {
		t.Fatalf("born %v is not within seconds of now (%v)", b, d)
	}
}

func TestBornOfMissingIsZero(t *testing.T) {
	if b := bornOf(t.TempDir() + "/nope"); !b.IsZero() {
		t.Fatalf("missing path must give the zero time, got %v", b)
	}
}
