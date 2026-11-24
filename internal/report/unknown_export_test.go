package report

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/als0x/sapulidi/internal/engine"
)

func TestScanUncoveredReturnsWhatReportShows(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.root, "mystery", "a.bin"), 8192)
	write(t, filepath.Join(f.env.Home, ".cache", "thing", "c.bin"), 4096)

	rep, err := Build(f.env, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := engine.Plan(f.env, nil, engine.Options{})
	got, partial := ScanUncovered(f.env, sum)
	if partial {
		t.Fatal("a finished scan must not be partial")
	}
	if len(got) == 0 || !reflect.DeepEqual(got, rep.Unknown) {
		t.Fatalf("wrapper %+v, report %+v", got, rep.Unknown)
	}
}

func TestScanUncoveredIsPartialWhenCancelled(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.root, "mystery", "a.bin"), 8192)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.env.Ctx = ctx
	_, partial := ScanUncovered(f.env, engine.Summary{})
	if !partial {
		t.Fatal("a cancelled scan must say partial")
	}
}
