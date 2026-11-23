package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/als0x/sapulidi/internal/exclude"
	"github.com/als0x/sapulidi/internal/testutil"
)

func writeExclude(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "sapulidi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, exclude.FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBuildEnvFailsClosedOnCorruptExclusions(t *testing.T) {
	home := testutil.TempHome(t)
	writeExclude(t, home, "[[exclude]\nthis is not toml")
	_, err := buildEnv()
	var ce *exclude.CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("buildEnv error = %v, want *exclude.CorruptError", err)
	}
}

func TestCommandsRefuseWithCorruptExclusions(t *testing.T) {
	home := testutil.TempHome(t)
	writeExclude(t, home, "[[exclude]\nthis is not toml")
	// Every command that builds an env or decides about deletions, check (the SessionStart hook) included.
	// `proposals` only lists, shows and rejects without an env; applying a clean-once proposal builds one.
	// `agent run` builds one only to look for growth anomalies and removes nothing.
	for _, args := range [][]string{{"plan"}, {"run"}, {"report"}, {"check"}, {"explain", "/some/path"}, {"procs"}} {
		var out, errb bytes.Buffer
		if code := Main(args, &out, &errb); code != 1 {
			t.Errorf("%v: exit %d, want 1 (stdout %q stderr %q)", args, code, out.String(), errb.String())
		}
		if !bytes.Contains(errb.Bytes(), []byte(exclude.FileName)) {
			t.Errorf("%v: stderr does not name the file: %q", args, errb.String())
		}
	}
}

func TestBuildEnvAcceptsMissingAndValidExclusions(t *testing.T) {
	home := testutil.TempHome(t)
	env, err := buildEnv()
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if env.Exclusions == nil {
		t.Fatal("buildEnv must always set Env.Exclusions, even for an empty list")
	}
	writeExclude(t, home, "[[exclude]]\npath = \"/some/where\"\nadded = \"2026-01-01\"\n")
	if _, err := buildEnv(); err != nil {
		t.Fatalf("valid file: %v", err)
	}
}
