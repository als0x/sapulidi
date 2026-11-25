package cli

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/als0x/sapulidi/internal/core"
)

// stderrIsTTY reports whether w is a terminal file descriptor.
// Tests replace it to exercise progress output without a real pty.
var stderrIsTTY = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return fdIsTTY(f.Fd())
}

// newScanProgress returns a core.Env.Progress callback that updates a single status line on w
// and a finish function that clears the line if anything was drawn.
// When !enabled or w == nil, progress is nil and finish is a no-op.
func newScanProgress(w io.Writer, enabled bool) (progress func(category string, done, total int), finish func()) {
	if !enabled || w == nil {
		return nil, func() {}
	}
	var mu sync.Mutex
	var drawn bool
	progress = func(category string, done, total int) {
		mu.Lock()
		defer mu.Unlock()
		if category != "" {
			fmt.Fprintf(w, "\r\x1b[2KScanning... %d/%d (%s)", done, total, category)
		} else {
			fmt.Fprintf(w, "\r\x1b[2KScanning... %d/%d", done, total)
		}
		drawn = true
	}
	finish = func() {
		mu.Lock()
		defer mu.Unlock()
		if drawn {
			fmt.Fprint(w, "\r\x1b[2K")
			drawn = false
		}
	}
	return progress, finish
}

// attachProgress wires TTY stderr progress into env when !app.JSON and app.Stderr is a terminal.
// The returned finish function clears the progress line (if drawn) and restores env.Progress.
func attachProgress(app *App, env *core.Env) func() {
	return attachProgressIf(app, env, true)
}

// attachProgressIf is attachProgress gated by an extra condition (e.g. disabled for quick or scheduled runs).
func attachProgressIf(app *App, env *core.Env, allow bool) func() {
	if !allow || app == nil || env == nil || app.JSON || !stderrIsTTY(app.Stderr) {
		return func() {}
	}
	prev := env.Progress
	cb, clear := newScanProgress(app.Stderr, true)
	env.Progress = cb
	var once sync.Once
	return func() {
		once.Do(func() {
			clear()
			env.Progress = prev
		})
	}
}
