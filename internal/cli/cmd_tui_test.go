package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/tui"
)

type noTermDeps struct{ err error }

func (d noTermDeps) Terminal() (tui.Terminal, error) { return nil, d.err }
func (noTermDeps) Scan(context.Context, func(string, int, int)) ([]tui.Row, error) {
	return nil, nil
}
func (noTermDeps) Summary() tui.SummaryData                    { return tui.SummaryData{} }
func (noTermDeps) Delete(context.Context, []string) tui.Result { return tui.Result{} }
func (noTermDeps) AddExclusion(string) error                   { return nil }
func (noTermDeps) RemoveExclusion(string) error                { return nil }

func TestTUIIsRegistered(t *testing.T) {
	if TUI == nil {
		t.Fatal("cli.TUI is not set")
	}
}

func TestRunTUIWithExitCodes(t *testing.T) {
	var out, errb bytes.Buffer
	app := &App{Stdout: &out, Stderr: &errb}
	if code := runTUIWith(app, noTermDeps{tui.ErrNoTerminal}); code != 0 || !strings.Contains(out.String(), "usage:") {
		t.Fatalf("no terminal: code %d out %q", code, out.String())
	}
	out.Reset()
	if code := runTUIWith(app, noTermDeps{errors.New("boom")}); code != 1 || !strings.Contains(errb.String(), "boom") {
		t.Fatalf("failure: code %d err %q", code, errb.String())
	}
}
