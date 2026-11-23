package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/version"
)

func TestMainUnknownCommandExits2(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Main([]string{"nonesuch"}, &out, &errb); code != 2 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "usage") {
		t.Fatalf("stderr %q", errb.String())
	}
}

func TestHelpListsCommandsSorted(t *testing.T) {
	Register(Command{Name: "zzz", Summary: "last summary"})
	Register(Command{Name: "aaa", Summary: "first summary"})
	defer delete(commands, "zzz")
	defer delete(commands, "aaa")
	var out, errb bytes.Buffer
	if code := Main([]string{"help"}, &out, &errb); code != 0 {
		t.Fatalf("code %d", code)
	}
	s := out.String()
	idx := []int{}
	for _, w := range []string{"aaa", "help", "version", "zzz"} {
		i := strings.Index(s, w)
		if i < 0 {
			t.Fatalf("%q missing:\n%s", w, s)
		}
		idx = append(idx, i)
	}
	for i := 1; i < len(idx); i++ {
		if idx[i-1] > idx[i] {
			t.Fatalf("not sorted:\n%s", s)
		}
	}
	if !strings.Contains(s, "first summary") || !strings.Contains(s, "last summary") {
		t.Fatalf("summaries missing:\n%s", s)
	}
}

func TestVersionJSON(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Main([]string{"--json", "version"}, &out, &errb); code != 0 {
		t.Fatalf("code %d", code)
	}
	var v map[string]string
	if err := json.Unmarshal(out.Bytes(), &v); err != nil || v["version"] != version.Version {
		t.Fatalf("out %q err %v", out.String(), err)
	}
}

func TestJSONFlagAfterCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Main([]string{"version", "--json"}, &out, &errb); code != 0 {
		t.Fatalf("code %d", code)
	}
	var v map[string]string
	if err := json.Unmarshal(out.Bytes(), &v); err != nil || v["version"] != version.Version {
		t.Fatalf("out %q err %v", out.String(), err)
	}
}

func TestVersionText(t *testing.T) {
	var out, errb bytes.Buffer
	Main([]string{"version"}, &out, &errb)
	if strings.TrimSpace(out.String()) != version.Version {
		t.Fatalf("out %q", out.String())
	}
}
