package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/als0x/sapulidi/internal/testutil"
)

func TestPauseResume(t *testing.T) {
	testutil.TempHome(t)
	st, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Main([]string{"pause"}, &out, &errb); code != 0 || !st.Paused() {
		t.Fatalf("pause: code %d paused %v %s", code, st.Paused(), errb.String())
	}
	out.Reset()
	if code := Main([]string{"--json", "resume"}, &out, &errb); code != 0 || st.Paused() {
		t.Fatalf("resume: code %d paused %v", code, st.Paused())
	}
	var doc struct{ Paused bool }
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || doc.Paused {
		t.Fatalf("%v %s", err, out.String())
	}
	if code := Main([]string{"pause", "now"}, &out, &errb); code != 2 {
		t.Fatalf("extra argument must be a usage error, got %d", code)
	}
}
