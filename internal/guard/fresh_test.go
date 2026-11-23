package guard

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/testutil"
)

func TestFreshNewestInsideHoldsFolder(t *testing.T) {
	t.Setenv("SAPULIDI_TEST", "1")
	t.Setenv("SAPULIDI_TEST_NO_CTIME", "1")
	d := t.TempDir()
	old := 72 * time.Hour
	testutil.Touch(t, filepath.Join(d, "a", "old"), old)
	testutil.Touch(t, filepath.Join(d, "a", "deep", "new"), time.Minute)
	testutil.Touch(t, filepath.Join(d, "a", "deep"), old)
	testutil.Touch(t, filepath.Join(d, "a"), old)
	testutil.Touch(t, d, old)
	now := time.Now()
	wantWhy(t, Fresh(d, 24*time.Hour, now), "young")
	n, err := Newest(d)
	if err != nil || now.Sub(n) > 5*time.Minute {
		t.Fatalf("newest %v %v", n, err)
	}
}

func TestFreshOld(t *testing.T) {
	t.Setenv("SAPULIDI_TEST", "1")
	t.Setenv("SAPULIDI_TEST_NO_CTIME", "1")
	d := t.TempDir()
	testutil.Touch(t, filepath.Join(d, "a", "f"), 72*time.Hour)
	testutil.Touch(t, filepath.Join(d, "a"), 72*time.Hour)
	testutil.Touch(t, d, 72*time.Hour)
	wantWhy(t, Fresh(d, 24*time.Hour, time.Now()), "")
	wantWhy(t, Fresh(filepath.Join(d, "gone"), time.Hour, time.Now()), "error")
}

func TestInUseErrorCountsAsOpen(t *testing.T) {
	wantWhy(t, InUse(&testutil.FakePlatform{}, "/x", true), "")
	wantWhy(t, InUse(&testutil.FakePlatform{Open: map[string]bool{"/x": true}}, "/x", false), "open")
	wantWhy(t, InUse(&testutil.FakePlatform{OpenErr: errors.New("timeout")}, "/x", false), "open")
}

func TestInUseArgs(t *testing.T) {
	p := &testutil.FakePlatform{Mentioned: map[string]bool{"/x": true}}
	wantWhy(t, InUse(p, "/x", false), "")
	wantWhy(t, InUse(p, "/x", true), "process")
	wantWhy(t, InUse(&testutil.FakePlatform{MentionErr: errors.New("boom")}, "/x", true), "process")
}
