package tui

import (
	"context"
	"fmt"
	"sync"

	"github.com/als0x/sapulidi/internal/tui/term"
)

// ErrNoTerminal is returned by Run when there is no terminal to draw on.
var ErrNoTerminal = term.ErrNoTerminal

// Terminal is the part of *term.Term the loop uses; tests pass a fake.
type Terminal interface {
	Enter() error
	Restore()
	Size() (cols, rows int, err error)
	Events() <-chan term.Event
	Write(lines []string)
	IsPlain() bool
}

// Deps is everything Run needs from the outside, so tests can use fakes.
type Deps interface {
	// Terminal opens the terminal; it returns ErrNoTerminal when there is none.
	Terminal() (Terminal, error)
	// Scan lists every row in one pass; progress reports finished categories. It returns early when ctx is done.
	Scan(ctx context.Context, progress func(category string, done, total int)) ([]Row, error)
	// Summary is the data of the summary panel besides the rows.
	Summary() SummaryData
	// Delete removes exactly paths, through the engine, and reports what happened.
	Delete(ctx context.Context, paths []string) Result
	AddExclusion(path string) error
	RemoveExclusion(path string) error
}

// runUpdate is Update; a test replaces it to inject a panic.
var runUpdate = Update

// statusMsg is an internal message that sets the status line.
type statusMsg string

// Run owns the loop: it opens the terminal, feeds key and resize events into Update, runs the effects and draws
// the frame after every message. The terminal is restored when Run returns or panics.
func Run(ctx context.Context, d Deps) error {
	t, err := d.Terminal()
	if err != nil {
		return err
	}
	if t == nil {
		return ErrNoTerminal
	}
	if err := t.Enter(); err != nil {
		return err
	}
	defer t.Restore()

	l := &loop{ctx: ctx, d: d, t: t, m: NewModel(t.IsPlain()), msgs: make(chan Msg, 64), status: make(chan statusMsg, 8), stop: make(chan struct{})}
	defer l.finish()    // runs second: waits for a running deletion
	defer close(l.stop) // runs first: background senders give up

	// The model needs the real size before the first key: an unreported size counts as large enough.
	if cols, rows, err := t.Size(); err == nil {
		l.w, l.h = cols, rows
		l.m, _ = runUpdate(l.m, ResizeMsg{Width: cols, Height: rows})
	}
	l.execute(Init())
	l.draw()

	events := t.Events()
	for !l.quit {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			switch e := ev.(type) {
			case term.Key:
				l.apply(KeyMsg{Rune: e.Rune, Special: specialName(e.Special), Ctrl: e.Ctrl})
			case term.Resize:
				l.w, l.h = e.Cols, e.Rows
				l.apply(ResizeMsg{Width: e.Cols, Height: e.Rows})
			}
		case msg := <-l.msgs:
			l.apply(msg)
		case s := <-l.status:
			l.m.Status = string(s)
			l.draw()
		}
	}
	return nil
}

func specialName(k term.SpecialKey) string {
	switch k {
	case term.KeyUp:
		return KeyUp
	case term.KeyDown:
		return KeyDown
	case term.KeyEnter:
		return KeyEnter
	case term.KeyEsc:
		return KeyEsc
	case term.KeyBackspace:
		return KeyBackspace
	case term.KeyNone:
		return ""
	}
	return "other" // a key the model has no use for must not look like a printable one
}

type loop struct {
	ctx    context.Context
	d      Deps
	t      Terminal
	m      Model
	w, h   int
	quit   bool
	msgs   chan Msg
	status chan statusMsg
	stop   chan struct{} // closed when Run returns: background senders give up

	scanCancel context.CancelFunc
	deleting   sync.WaitGroup
	delCancel  context.CancelFunc
}

// finish stops the work in flight; a running deletion is waited for so that no removal is cut off half way.
func (l *loop) finish() {
	if l.scanCancel != nil {
		l.scanCancel()
	}
	if l.delCancel != nil {
		l.delCancel()
	}
	l.deleting.Wait()
}

func (l *loop) send(m Msg) {
	select {
	case l.msgs <- m:
	case <-l.stop:
	}
}

func (l *loop) say(s string) {
	select {
	case l.status <- statusMsg(s):
	case <-l.stop:
	}
}

func (l *loop) apply(msg Msg) {
	var eff []Effect
	l.m, eff = runUpdate(l.m, msg)
	l.execute(eff)
	l.draw()
}

func (l *loop) draw() {
	w, h := l.w, l.h
	if w == 0 || h == 0 {
		w, h = minWidth, minHeight
	}
	l.t.Write(View(l.m, w, h))
}

func (l *loop) execute(effects []Effect) {
	for _, e := range effects {
		switch e := e.(type) {
		case StartScan:
			l.startScan()
		case CancelScan:
			if l.scanCancel != nil {
				l.scanCancel()
			}
		case Delete:
			l.startDelete(e.Paths)
		case AddExclusion:
			l.exclusion(e.Path, true, l.d.AddExclusion)
		case RemoveExclusion:
			l.exclusion(e.Path, false, l.d.RemoveExclusion)
		case Quit:
			l.quit = true
		}
	}
}

func (l *loop) exclusion(path string, excluded bool, do func(string) error) {
	if err := do(path); err != nil {
		l.m.Status = "exclusion not changed, nothing deleted: " + err.Error()
		return
	}
	var eff []Effect
	l.m, eff = runUpdate(l.m, ExclusionChangedMsg{Path: path, Excluded: excluded})
	l.execute(eff)
}

func (l *loop) startScan() {
	ctx, cancel := context.WithCancel(l.ctx)
	l.scanCancel = cancel
	go func() {
		defer func() {
			if p := recover(); p != nil {
				l.send(ScanDoneMsg{})
				l.say(fmt.Sprintf("scan failed: %v", p))
			}
		}()
		rows, err := l.d.Scan(ctx, func(cat string, done, total int) {
			if ctx.Err() == nil {
				l.send(ScanProgressMsg{Category: cat, Done: done, Total: total})
			}
		})
		if ctx.Err() != nil {
			return // cancelled: the model already shows an empty list
		}
		l.send(ScanDoneMsg{Rows: rows, Summary: l.d.Summary()})
		if err != nil {
			l.say("scan failed: " + err.Error())
		}
	}()
}

func (l *loop) startDelete(paths []string) {
	ctx, cancel := context.WithCancel(l.ctx)
	l.delCancel = cancel
	l.deleting.Add(1)
	go func() {
		defer l.deleting.Done()
		var res Result
		func() {
			defer func() {
				if p := recover(); p != nil {
					res = Result{Err: fmt.Sprintf("deletion failed: %v", p)}
				}
			}()
			res = l.d.Delete(ctx, paths)
		}()
		l.send(DeleteDoneMsg{Result: res})
	}()
}
