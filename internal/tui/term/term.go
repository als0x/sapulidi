// Package term is the thin OS-specific layer under the TUI: raw mode, alternate screen, size,
// signals and key decoding. It holds no TUI logic.
//
// Restore runs on normal return, on a panic in the goroutine that called Run, and on SIGINT,
// SIGTERM, SIGHUP and SIGQUIT. A panic in any other goroutine kills the process without passing
// through Run: that goroutine must recover, or call Restore itself, before it re-panics.
//
// Limit: the input reader goroutine blocks in Read and cannot be cancelled, so it may swallow one
// byte of input after Restore. The terminal's stdin must not be reused after Restore.
package term

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// ErrNoTerminal is returned by Open when there is no terminal to draw on.
var ErrNoTerminal = errors.New("term: no terminal available")

const (
	escTimeout = 50 * time.Millisecond

	enterSeq   = "\x1b[?1049h\x1b[?25l\x1b[?7l" // alternate screen, hide cursor, autowrap off
	restoreSeq = "\x1b[?7h\x1b[?25h\x1b[?1049l" // autowrap on, show cursor, main screen
)

// Term is a terminal in or out of raw mode. Plain is true when the view must not use colour
// (NO_COLOR set, or TERM=dumb).
type Term struct {
	Plain bool

	in, out *os.File
	events  chan Event
	done    chan struct{}
	exit    func(int)

	mu      sync.Mutex
	orig    syscall.Termios
	entered bool
	sigs    chan os.Signal
}

// Open opens /dev/tty, or uses stdin and stdout when both are terminals. It returns ErrNoTerminal
// otherwise.
func Open() (*Term, error) {
	return open("/dev/tty", os.Stdin, os.Stdout)
}

func open(ttyPath string, stdin, stdout *os.File) (*Term, error) {
	if f, err := os.OpenFile(ttyPath, os.O_RDWR, 0); err == nil {
		if isTerminal(f) {
			return newTerm(f, f), nil
		}
		f.Close()
	}
	if isTerminal(stdin) && isTerminal(stdout) {
		return newTerm(stdin, stdout), nil
	}
	return nil, ErrNoTerminal
}

func newTerm(in, out *os.File) *Term {
	return &Term{
		Plain:  os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb",
		in:     in,
		out:    out,
		events: make(chan Event, 64),
		done:   make(chan struct{}),
		exit:   os.Exit,
	}
}

func ioctl(fd, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

func getTermios(f *os.File) (syscall.Termios, error) {
	var t syscall.Termios
	err := ioctl(f.Fd(), ioctlGetTermios, unsafe.Pointer(&t))
	return t, err
}

func setTermios(f *os.File, t *syscall.Termios) error {
	return ioctl(f.Fd(), ioctlSetTermios, unsafe.Pointer(t))
}

func isTerminal(f *os.File) bool {
	_, err := getTermios(f)
	return err == nil
}

// Enter switches to raw mode and the alternate screen with a hidden cursor, and starts delivering
// events. Calling it again while entered does nothing. Pair it with Restore, or use Run.
func (t *Term) Enter() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entered {
		return nil
	}
	select {
	case <-t.done:
		return errors.New("term: a Term cannot be entered again after Restore")
	default:
	}
	orig, err := getTermios(t.in)
	if err != nil {
		return fmt.Errorf("term: read terminal mode: %w", err)
	}
	// Signals are caught before the terminal changes, so none can kill the process while it is raw.
	// They wait in the buffer (and signalLoop in t.mu) until Enter returns.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGWINCH)
	t.sigs = sigs
	raw := orig
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := setTermios(t.in, &raw); err != nil {
		signal.Stop(sigs)
		t.sigs = nil
		return fmt.Errorf("term: enter raw mode: %w", err)
	}
	t.orig = orig
	t.entered = true
	if _, err := t.out.WriteString(enterSeq); err != nil {
		t.restoreLocked()
		return fmt.Errorf("term: write: %w", err)
	}
	go t.signalLoop(sigs)
	go t.readLoop()
	return nil
}

// Restore leaves the alternate screen, shows the cursor and puts the original terminal mode back.
// It is safe to call more than once.
func (t *Term) Restore() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.restoreLocked()
}

func (t *Term) restoreLocked() {
	if !t.entered {
		return
	}
	t.entered = false
	if t.sigs != nil {
		signal.Stop(t.sigs)
		close(t.sigs) // Stop guarantees no more sends; this ends signalLoop
		t.sigs = nil
	}
	select {
	case <-t.done:
	default:
		close(t.done)
	}
	_, _ = t.out.WriteString(restoreSeq)
	_ = setTermios(t.in, &t.orig)
}

// Run enters raw mode, calls fn and restores the terminal when fn returns or panics (the panic
// continues after the restore).
func (t *Term) Run(fn func() error) error {
	if err := t.Enter(); err != nil {
		return err
	}
	defer func() {
		t.Restore()
		if p := recover(); p != nil {
			panic(p)
		}
	}()
	return fn()
}

// Size returns the terminal size in columns and rows.
func (t *Term) Size() (cols, rows int, err error) {
	var ws struct{ Row, Col, X, Y uint16 }
	if err := ioctl(t.out.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil {
		return 0, 0, fmt.Errorf("term: size: %w", err)
	}
	if ws.Col == 0 || ws.Row == 0 {
		return 0, 0, errors.New("term: size: terminal reports zero size")
	}
	return int(ws.Col), int(ws.Row), nil
}

// Events delivers Key and Resize events after Enter.
func (t *Term) Events() <-chan Event { return t.events }

// Write draws a full frame: cursor home, every line cleared to its end, the rest of the screen
// cleared, no scrolling. The caller keeps each line within the terminal width.
//
// Write does nothing unless the terminal is entered, and lines beyond the current row count are
// cut so a frame never scrolls.
func (t *Term) Write(lines []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.entered {
		return
	}
	if _, rows, err := t.Size(); err == nil && len(lines) > rows {
		lines = lines[:rows]
	}
	var b strings.Builder
	b.WriteString("\x1b[H")
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(l)
		b.WriteString("\x1b[K")
	}
	b.WriteString("\x1b[J")
	_, _ = t.out.WriteString(b.String())
}

func (t *Term) send(e Event) {
	select {
	case t.events <- e:
	case <-t.done:
	}
}

func (t *Term) signalLoop(sigs <-chan os.Signal) {
	for s := range sigs {
		switch s {
		case syscall.SIGWINCH:
			if c, r, err := t.Size(); err == nil {
				select {
				case t.events <- Resize{Cols: c, Rows: r}:
				default: // a full queue already holds resizes; the view reads Size again
				}
			}
		default:
			t.mu.Lock()
			if !t.entered {
				// A normal Restore already ran; the caller is cleaning up, so drop the signal.
				t.mu.Unlock()
				return
			}
			t.restoreLocked()
			t.mu.Unlock()
			code := 128
			if n, ok := s.(syscall.Signal); ok {
				code += int(n)
			}
			t.exit(code)
			return
		}
	}
}

func (t *Term) readLoop() {
	chunks := make(chan []byte)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := t.in.Read(buf)
			if n > 0 {
				select {
				case chunks <- append([]byte(nil), buf[:n]...):
				case <-t.done:
					return
				}
			}
			if err != nil {
				close(chunks)
				return
			}
		}
	}()
	var pending []byte
	emit := func(final bool) {
		keys, n := Decode(pending, final)
		pending = pending[n:]
		if len(pending) == 0 {
			pending = nil
		}
		for _, k := range keys {
			t.send(k)
		}
	}
	for {
		var timeout <-chan time.Time
		if len(pending) > 0 {
			timeout = time.After(escTimeout)
		}
		select {
		case c, ok := <-chunks:
			if !ok {
				return
			}
			pending = append(pending, c...)
			emit(false)
		case <-timeout:
			emit(true)
		case <-t.done:
			return
		}
	}
}
