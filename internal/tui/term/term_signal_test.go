package term

import (
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

func nullTerm(t *testing.T) *Term {
	t.Helper()
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { null.Close() })
	return newTerm(null, null)
}

// The signal hook runs first and may block; only after it returns is the terminal restored and the process asked to
// exit with the conventional code.
func TestSignalHookRunsBeforeRestoreAndExit(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT} {
		t.Run(sig.String(), func(t *testing.T) {
			tm := nullTerm(t)
			tm.entered = true
			var mu sync.Mutex
			var order []string
			note := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }
			exited := make(chan int, 1)
			tm.exit = func(code int) { note("exit"); exited <- code }
			release := make(chan struct{})
			tm.OnSignal(func(os.Signal) {
				note("hook")
				<-release // the hook is still working
				note("hook done")
			})
			sigs := make(chan os.Signal, 1)
			go tm.signalLoop(sigs)
			sigs <- sig
			select {
			case <-exited:
				t.Fatal("exit before the hook returned")
			case <-time.After(100 * time.Millisecond):
			}
			if !tm.entered {
				t.Fatal("restored before the hook returned")
			}
			close(release)
			select {
			case code := <-exited:
				if code != 128+int(sig) {
					t.Fatalf("exit code %d", code)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("no exit")
			}
			mu.Lock()
			defer mu.Unlock()
			if len(order) != 3 || order[0] != "hook" || order[1] != "hook done" || order[2] != "exit" {
				t.Fatalf("order %v", order)
			}
			if tm.entered {
				t.Fatal("not restored")
			}
		})
	}
}

// A read error (the tty is gone) ends the event stream with Closed instead of leaving the caller waiting.
func TestReadErrorDeliversClosed(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	tm := newTerm(r, w)
	go tm.readLoop()
	w.WriteString("q")
	w.Close() // end of input
	var got []Event
	for len(got) < 2 {
		select {
		case e := <-tm.Events():
			got = append(got, e)
		case <-time.After(2 * time.Second):
			t.Fatalf("events so far: %v", got)
		}
	}
	if got[0] != (Key{Rune: 'q'}) || got[1] != (Closed{}) {
		t.Fatalf("events %v", got)
	}
}

// Resizes are never dropped, however full the event queue: the latest size is delivered when the reader catches up.
func TestResizeIsCoalescedNotDropped(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	tm := newTerm(r, w)
	for i := 0; i < cap(tm.events); i++ { // the queue is full
		tm.events <- Key{Rune: 'x'}
	}
	go tm.readLoop()
	for _, size := range []Resize{{80, 24}, {100, 30}, {120, 40}} {
		tm.resizeMu.Lock()
		s := size
		tm.resize = &s
		tm.resizeMu.Unlock()
		select {
		case tm.resizeKick <- struct{}{}:
		default:
		}
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-tm.Events():
			if rs, ok := e.(Resize); ok {
				if rs != (Resize{120, 40}) {
					t.Fatalf("got %v, want the latest size", rs)
				}
				return
			}
		case <-deadline:
			t.Fatal("the resize was dropped")
		}
	}
}
