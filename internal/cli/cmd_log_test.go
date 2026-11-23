package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/testutil"
)

var errNotFound = errors.New("not found")

func TestLogLimitAndJSON(t *testing.T) {
	testutil.TempHome(t)
	st, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		st.Logger().Event("summary", map[string]any{"n": i})
	}
	var out, errb bytes.Buffer
	if code := Main([]string{"log", "--limit", "2"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "4") || !strings.Contains(lines[1], "5") {
		t.Fatalf("want the last two events, got %q", lines)
	}
	out.Reset()
	if code := Main([]string{"--json", "log", "--limit", "3"}, &out, &errb); code != 0 {
		t.Fatalf("%d", code)
	}
	var doc struct {
		Events []map[string]any
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || len(doc.Events) != 3 || doc.Events[2]["n"] != float64(5) {
		t.Fatalf("%v %s", err, out.String())
	}
	if code := Main([]string{"log", "--limit", strconv.Itoa(-1)}, &out, &errb); code != 2 {
		t.Fatalf("a negative limit is a usage error, got %d", code)
	}
}

func TestLogSurvivesAnOversizeLine(t *testing.T) {
	testutil.TempHome(t)
	st, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	st.Logger().Event("summary", map[string]any{"n": 1})
	p, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(p.State, "log.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	huge := `{"kind":"x","blob":"` + strings.Repeat("a", 2<<20) + `"}` + "\n" // 2 MB on one line
	// Written by hand: the logger rotates a file that has reached 1 MB before its next event.
	if _, err := f.WriteString(huge + `{"kind":"summary","n":2}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	var out, errb bytes.Buffer
	if code := Main([]string{"--json", "log"}, &out, &errb); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	var doc struct{ Events []map[string]any }
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || len(doc.Events) != 2 || doc.Events[1]["n"] != float64(2) {
		t.Fatalf("%v, %d events", err, len(doc.Events))
	}
}

func TestLogEmpty(t *testing.T) {
	testutil.TempHome(t)
	var out, errb bytes.Buffer
	if code := Main([]string{"--json", "log"}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	var doc struct{ Events []any }
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || doc.Events == nil {
		t.Fatalf("%v %s", err, out.String())
	}
}
