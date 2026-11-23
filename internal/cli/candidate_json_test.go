package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
)

func TestCandidateJSONTimesAndSource(t *testing.T) {
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	b, err := json.Marshal(candidatesJSON([]core.Candidate{
		{Category: "temp", RuleKey: "x/a@1", Path: "/p", Bytes: 5, Created: old, Updated: old.Add(time.Hour), Source: "auto"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"created":"2026-01-02T03:04:05Z"`, `"updated":"2026-01-02T04:04:05Z"`, `"source":"auto"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
}

func TestCandidateJSONUnmeasuredOmitsKeys(t *testing.T) {
	b, err := json.Marshal(candidatesJSON([]core.Candidate{{Category: "temp", RuleKey: "x/a@1", Path: "/p", Bytes: -1}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"created", "updated", "source"} {
		if strings.Contains(string(b), key) {
			t.Fatalf("%s must be omitted: %s", key, b)
		}
	}
}
