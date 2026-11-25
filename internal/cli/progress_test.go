package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/engine"
	"github.com/als0x/sapulidi/internal/testutil"
)

func withStderrTTY(t *testing.T, isTTY bool) {
	t.Helper()
	old := stderrIsTTY
	stderrIsTTY = func(io.Writer) bool { return isTTY }
	t.Cleanup(func() { stderrIsTTY = old })
}

func TestProgressFormattingAndClear(t *testing.T) {
	var buf bytes.Buffer
	progress, finish := newScanProgress(&buf, true)
	if progress == nil {
		t.Fatal("expected non-nil progress callback")
	}
	progress("temp", 1, 19)
	progress("deps", 4, 19)
	progress("", 5, 19)
	finish()
	finish() // idempotent: second call must not emit another clear

	want := "\r\x1b[2KScanning... 1/19 (temp)\r\x1b[2KScanning... 4/19 (deps)\r\x1b[2KScanning... 5/19\r\x1b[2K"
	if got := buf.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestProgressFinishWithoutDrawWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	_, finish := newScanProgress(&buf, true)
	finish()
	if buf.Len() != 0 {
		t.Fatalf("expected empty output when nothing was drawn, got %q", buf.String())
	}
}

func TestProgressDisabledWhenJSONOrNonTTY(t *testing.T) {
	t.Run("disabled constructor", func(t *testing.T) {
		var buf bytes.Buffer
		progress, finish := newScanProgress(&buf, false)
		if progress != nil {
			t.Fatal("expected nil progress when disabled")
		}
		finish()
		if buf.Len() != 0 {
			t.Fatalf("unexpected output %q", buf.String())
		}
	})

	t.Run("non-TTY buffer", func(t *testing.T) {
		var buf bytes.Buffer
		env := &core.Env{}
		app := &App{Stderr: &buf}
		finish := attachProgress(app, env)
		if env.Progress != nil {
			t.Fatal("expected nil Progress for non-TTY buffer")
		}
		finish()
		if buf.Len() != 0 {
			t.Fatalf("unexpected output %q", buf.String())
		}
	})

	t.Run("dev-null is not a TTY", func(t *testing.T) {
		null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer null.Close()
		if stderrIsTTY(null) {
			t.Fatal("/dev/null should not be detected as a TTY")
		}
	})

	t.Run("JSON mode suppresses progress even on TTY", func(t *testing.T) {
		withStderrTTY(t, true)
		var buf bytes.Buffer
		env := &core.Env{}
		app := &App{Stderr: &buf, JSON: true}
		finish := attachProgress(app, env)
		if env.Progress != nil {
			t.Fatal("expected nil Progress when app.JSON is true")
		}
		finish()
		if buf.Len() != 0 {
			t.Fatalf("unexpected output %q", buf.String())
		}
	})

	t.Run("allow=false suppresses progress even on TTY", func(t *testing.T) {
		withStderrTTY(t, true)
		var buf bytes.Buffer
		env := &core.Env{}
		app := &App{Stderr: &buf}
		finish := attachProgressIf(app, env, false)
		if env.Progress != nil {
			t.Fatal("expected nil Progress when allow is false")
		}
		finish()
	})
}

func TestProgressConcurrentCalls(t *testing.T) {
	var buf bytes.Buffer
	progress, finish := newScanProgress(&buf, true)
	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func(n int) {
			defer wg.Done()
			progress("deps", n+1, workers)
		}(i)
	}
	wg.Wait()
	finish()
	got := buf.String()
	if !strings.Contains(got, "Scanning... ") {
		t.Fatalf("expected progress output, got %q", got)
	}
	if !strings.HasSuffix(got, "\r\x1b[2K") {
		t.Fatalf("expected trailing clear sequence, got %q", got)
	}
}

func TestProgressPlanIntegration(t *testing.T) {
	labelFixture(t)
	withStderrTTY(t, true)
	origPlan := planFn
	planFn = func(env *core.Env, cats []core.Category, opt engine.Options) engine.Summary {
		if env.Progress != nil {
			env.Progress("temp", 1, 2)
			env.Progress("deps", 2, 2)
		}
		return origPlan(env, cats, opt)
	}

	code, _, errOut := runCLI(t, "plan")
	if code != 0 {
		t.Fatalf("plan failed: %d %q", code, errOut)
	}
	want := "\r\x1b[2KScanning... 1/2 (temp)\r\x1b[2KScanning... 2/2 (deps)\r\x1b[2K"
	if errOut != want {
		t.Fatalf("stderr = %q, want %q", errOut, want)
	}

	// JSON mode must not emit progress.
	code, _, errOut = runCLI(t, "--json", "plan")
	if code != 0 || errOut != "" {
		t.Fatalf("--json plan: code=%d stderr=%q", code, errOut)
	}
}

func TestProgressRunIntegration(t *testing.T) {
	t.Run("interactive dry-run shows and clears progress", func(t *testing.T) {
		runFixture(t)
		withStderrTTY(t, true)
		code, out, errOut := runCLI(t, "run", "--dry-run")
		if code != 0 || !strings.Contains(out, "dry run") {
			t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
		}
		if !strings.Contains(errOut, "\r\x1b[2KScanning... 1/3 (temp)") ||
			!strings.Contains(errOut, "\r\x1b[2KScanning... 3/3 (worktrees)") ||
			!strings.HasSuffix(errOut, "\r\x1b[2K") {
			t.Fatalf("unexpected stderr %q", errOut)
		}
	})

	t.Run("quick run suppresses progress", func(t *testing.T) {
		runFixture(t)
		withStderrTTY(t, true)
		code, _, errOut := runCLI(t, "run", "--dry-run", "--quick")
		if code != 0 || errOut != "" {
			t.Fatalf("code=%d err=%q", code, errOut)
		}
	})

	t.Run("scheduled run suppresses progress", func(t *testing.T) {
		runFixture(t)
		withStderrTTY(t, true)
		code, _, errOut := runCLI(t, "run", "--dry-run", "--trigger", "schedule")
		if code != 0 || errOut != "" {
			t.Fatalf("code=%d err=%q", code, errOut)
		}
	})

	t.Run("json run suppresses progress", func(t *testing.T) {
		runFixture(t)
		withStderrTTY(t, true)
		code, _, errOut := runCLI(t, "--json", "run", "--dry-run")
		if code != 0 || errOut != "" {
			t.Fatalf("code=%d err=%q", code, errOut)
		}
	})

	t.Run("recommended dry-run shows and clears progress", func(t *testing.T) {
		runFixture(t)
		withStderrTTY(t, true)
		recordingTTY(t, "y\n")
		code, out, errOut := runCLI(t, "run", "--recommended", "--dry-run")
		if code != 0 || !strings.Contains(out, "recommended: 1 item(s)") {
			t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
		}
		if !strings.Contains(errOut, "\r\x1b[2KScanning... 1/3 (temp)") ||
			!strings.HasSuffix(errOut, "\r\x1b[2K") {
			t.Fatalf("unexpected stderr %q", errOut)
		}
	})
}

func TestProgressReportIntegration(t *testing.T) {
	home := testutil.TempHome(t)
	t.Setenv("SAPULIDI_TEST", "1")
	tmp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	env := &core.Env{
		Exclusions: testutil.NoExclusions,
		Home:       home,
		Now:        time.Now(),
		Ctx:        context.Background(),
		TempDir:    tmp,
		Platform:   &testutil.FakePlatform{Temp: tmp, Free: 1 << 40},
		Run:        &testutil.FakeRunner{Paths: map[string]string{}},
		Log:        &testutil.MemLogger{},
		Settings:   &testutil.FakeSettings{Categories: map[string]bool{}},
	}
	useBuild(t, env, []core.Category{
		stubCat{name: "temp", bucket: core.Auto},
		stubCat{name: "deps", bucket: core.Optional},
	})

	withStderrTTY(t, true)
	code, _, errOut := runCLI(t, "report")
	if code != 0 {
		t.Fatalf("report failed: code=%d err=%q", code, errOut)
	}
	want := "\r\x1b[2KScanning... 1/2 (temp)\r\x1b[2KScanning... 2/2 (deps)\r\x1b[2K"
	if errOut != want {
		t.Fatalf("stderr = %q, want %q", errOut, want)
	}

	code, _, errOut = runCLI(t, "--json", "report")
	if code != 0 || errOut != "" {
		t.Fatalf("--json report: code=%d err=%q", code, errOut)
	}
}
