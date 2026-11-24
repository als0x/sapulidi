package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/als0x/sapulidi/internal/tui/term"
)

// ErrNoTerminal is returned by Run when there is no terminal to draw on.
var ErrNoTerminal = term.ErrNoTerminal

// errClosed is returned by Run when the terminal's input is gone (the tty was closed under the program).
var errClosed = errors.New("the terminal was closed")

// finishWait is how long Run waits for a deletion in flight when it is asked to stop.
var finishWait = 10 * time.Second

// Terminal is the part of *term.Term the loop uses; tests pass a fake. A terminal may also implement
// `OnSignal(func(os.Signal))`: Run then registers a hook that finishes a running deletion before the process exits
// on SIGINT, SIGTERM, SIGHUP or SIGQUIT.
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

// UncoveredDeps is the optional second phase of the scan: Deps that also implement it get the big folders no rule
// covers searched after the places are shown. It returns early (partial) when ctx is done. Deps without it report
// an empty, finished search.
type UncoveredDeps interface {
	Uncovered(ctx context.Context) (dirs []Uncovered, partial bool)
}

// runUpdate is Update; a test replaces it to inject a panic.
var runUpdate = Update

// statusMsg is an internal message that sets the status line.
type statusMsg string

// Run owns the loop: it opens the terminal, feeds key and resize events into Update, runs the effects and draws
// the frame after every message. The terminal is restored when Run returns or panics.
func Run(parent context.Context, d Deps) error {
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

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	l := &loop{ctx: ctx, d: d, t: t, m: NewModel(t.IsPlain()), msgs: make(chan Msg, 64), status: make(chan statusMsg, 8), stop: make(chan struct{})}
	defer l.finish()    // runs second: waits for a running deletion
	defer close(l.stop) // runs first: background senders give up

	if h, ok := t.(interface{ OnSignal(func(os.Signal)) }); ok {
		h.OnSignal(func(os.Signal) {
			cancel()
			l.showFinishing()
			l.waitDeletion(finishWait)
		})
	}

	// The model needs a size before the first key. Without one the window counts as too small, so `d` stays off
	// until a real size arrives.
	if cols, rows, err := t.Size(); err == nil {
		l.w, l.h = cols, rows
		l.m, _ = runUpdate(l.m, ResizeMsg{Width: cols, Height: rows})
	} else {
		l.m, _ = runUpdate(l.m, ResizeMsg{Width: 1, Height: 1})
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
				return errClosed
			}
			switch e := ev.(type) {
			case term.Key:
				l.apply(KeyMsg{Rune: e.Rune, Special: specialName(e.Special), Ctrl: e.Ctrl})
			case term.Resize:
				l.w, l.h = e.Cols, e.Rows
				l.apply(ResizeMsg{Width: e.Cols, Height: e.Rows})
			case term.Closed:
				return errClosed
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

	mu         sync.Mutex // guards the fields below; the signal hook runs on another goroutine
	scanCancel context.CancelFunc
	uncCancel  context.CancelFunc
	delCancel  context.CancelFunc
	delDone    chan struct{} // closed when the running deletion has returned; nil when none ran
}

// deleting reports whether a deletion is in flight.
func (l *loop) deleting() bool {
	l.mu.Lock()
	done := l.delDone
	l.mu.Unlock()
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

// waitDeletion waits for the running deletion, at most d.
func (l *loop) waitDeletion(d time.Duration) {
	l.mu.Lock()
	done := l.delDone
	l.mu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(d):
	}
}

// showFinishing draws the frame that tells the user why the program is still there.
func (l *loop) showFinishing() {
	if l.deleting() {
		l.t.Write([]string{"finishing the deletion, please wait..."})
	}
}

// finish stops the work in flight; a running deletion is waited for (a while) so that no removal is cut off.
func (l *loop) finish() {
	l.mu.Lock()
	scan, unc, del := l.scanCancel, l.uncCancel, l.delCancel
	l.mu.Unlock()
	if scan != nil {
		scan()
	}
	if unc != nil {
		unc()
	}
	if del != nil {
		del()
	}
	l.waitDeletion(finishWait)
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
	if !l.quit { // a finishing frame may be on screen; do not draw over it
		l.draw()
	}
}

// draw writes the frame. The size is read again each time: a resize event may have been coalesced or lost.
func (l *loop) draw() {
	if c, r, err := l.t.Size(); err == nil && (c != l.w || r != l.h) {
		l.w, l.h = c, r
		l.m, _ = runUpdate(l.m, ResizeMsg{Width: c, Height: r})
	}
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
			l.mu.Lock()
			cancel := l.scanCancel
			l.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		case StartUncovered:
			l.startUncovered()
		case CancelUncovered:
			l.mu.Lock()
			cancel := l.uncCancel
			l.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		case Delete:
			l.startDelete(e.Paths)
		case AddExclusion:
			if msg := l.refuseExclusion(e.Path); msg != "" {
				l.m.Status = msg
				continue
			}
			l.exclusion(e.Path, true, l.d.AddExclusion)
		case RemoveExclusion:
			l.exclusion(e.Path, false, l.d.RemoveExclusion)
		case Quit:
			l.quit = true
			l.showFinishing() // a deletion is waited for after the loop: say so
		}
	}
}

// refuseExclusion says why path cannot be stored as an exclusion, or "". Only a listed absolute path of a row that
// can be selected is a file or folder; a command text (a wholesale row) is not, and a `*` would become a glob.
func (l *loop) refuseExclusion(path string) string {
	for _, r := range l.m.Rows {
		if r.Path == path && r.Block != "" {
			return "not a path: nothing to exclude"
		}
	}
	switch {
	case !filepath.IsAbs(path):
		return "not a path: nothing to exclude"
	case strings.Contains(path, "*"):
		return "a path with * cannot be excluded here: it would match more than this folder"
	}
	return ""
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
	l.mu.Lock()
	l.scanCancel = cancel
	l.mu.Unlock()
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

// startUncovered runs the second phase in its own goroutine with its own cancel; its only output is one message.
func (l *loop) startUncovered() {
	ctx, cancel := context.WithCancel(l.ctx)
	l.mu.Lock()
	prev := l.uncCancel
	l.uncCancel = cancel
	l.mu.Unlock()
	if prev != nil {
		prev()
	}
	go func() {
		msg := UncoveredMsg{}
		func() {
			defer func() {
				if p := recover(); p != nil {
					msg = UncoveredMsg{Partial: true}
				}
			}()
			if u, ok := l.d.(UncoveredDeps); ok {
				msg.Unknown, msg.Partial = u.Uncovered(ctx)
				msg.Partial = msg.Partial || ctx.Err() != nil
			}
		}()
		l.send(msg)
	}()
}

func (l *loop) startDelete(paths []string) {
	ctx, cancel := context.WithCancel(l.ctx)
	done := make(chan struct{})
	l.mu.Lock()
	l.delCancel, l.delDone = cancel, done
	l.mu.Unlock()
	go func() {
		defer close(done)
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
