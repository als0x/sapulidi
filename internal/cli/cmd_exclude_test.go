package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/testutil"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(args, &out, &errb)
	return code, out.String(), errb.String()
}

func withTerminal(t *testing.T, has bool, typed string) {
	t.Helper()
	oldTTY, oldIs := confirm.OpenTTY, confirm.StdioIsTerminal
	t.Cleanup(func() { confirm.OpenTTY, confirm.StdioIsTerminal = oldTTY, oldIs })
	confirm.StdioIsTerminal = func() bool { return has }
	confirm.OpenTTY = func() (io.ReadWriteCloser, error) { return fakeTTY{strings.NewReader(typed)}, nil }
}

func TestExcludeAddListRemove(t *testing.T) {
	home := testutil.TempHome(t)
	dir := filepath.Join(home, "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	withTerminal(t, false, "") // add needs no terminal

	if code, out, e := runCLI(t, "exclude", dir, "--note", "keep me"); code != 0 || !strings.Contains(out, "excluded") {
		t.Fatalf("add: %d %q %q", code, out, e)
	}
	if code, out, _ := runCLI(t, "exclude", dir); code != 0 || !strings.Contains(out, "already excluded") {
		t.Fatalf("again: %d %q", code, out)
	}
	code, out, _ := runCLI(t, "exclude", "--list")
	if code != 0 || !strings.Contains(out, "keep me") || !strings.Contains(out, "proj") {
		t.Fatalf("list: %d %q", code, out)
	}
	code, out, _ = runCLI(t, "exclude", "--list", "--json")
	var doc struct {
		Exclusions []struct{ Path, Added, Note string } `json:"exclusions"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 0 || len(doc.Exclusions) != 1 || doc.Exclusions[0].Note != "keep me" {
		t.Fatalf("json: %d %q %v", code, out, err)
	}

	// remove without a terminal refuses and changes nothing
	code, _, e := runCLI(t, "exclude", "--remove", dir)
	if code != 1 || !strings.Contains(e, "run this in your own terminal") {
		t.Fatalf("remove no tty: %d %q", code, e)
	}
	if _, out, _ := runCLI(t, "exclude", "--list"); !strings.Contains(out, "proj") {
		t.Fatalf("entry vanished: %q", out)
	}
	// wrong word
	withTerminal(t, true, "no\n")
	if code, _, _ := runCLI(t, "exclude", "--remove", dir); code != 1 {
		t.Fatalf("mismatch: %d", code)
	}
	// confirmed
	withTerminal(t, true, "remove\n")
	if code, out, e := runCLI(t, "exclude", "--remove", dir); code != 0 {
		t.Fatalf("remove: %d %q %q", code, out, e)
	}
	if _, out, _ := runCLI(t, "exclude", "--list"); !strings.Contains(out, "no exclusions") {
		t.Fatalf("not removed: %q", out)
	}
	// unknown path
	withTerminal(t, true, "remove\n")
	if code, _, _ := runCLI(t, "exclude", "--remove", dir); code != 1 {
		t.Fatalf("unknown: %d", code)
	}
}

func TestExcludeUsageAndCorrupt(t *testing.T) {
	home := testutil.TempHome(t)
	for _, args := range [][]string{{"exclude"}, {"exclude", "--note"}, {"exclude", "--list", "x"}, {"exclude", "--remove"}, {"exclude", "--bogus"}} {
		if code, _, _ := runCLI(t, args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
	f := filepath.Join(home, ".config", "sapulidi", "exclude.toml")
	if err := os.WriteFile(f, []byte("[[exclude\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, e := runCLI(t, "exclude", "--list")
	if code != 1 || !strings.Contains(e, "nothing will run until it is fixed") {
		t.Fatalf("corrupt: %d %q", code, e)
	}
}
