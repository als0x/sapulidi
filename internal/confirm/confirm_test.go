package confirm

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// fakeTTY is a scripted terminal: reads come from in, writes land in out.
type fakeTTY struct {
	in     io.Reader
	out    bytes.Buffer
	closed bool
	read   int
}

func (f *fakeTTY) Read(p []byte) (int, error) {
	n, err := f.in.Read(p)
	f.read += n
	return n, err
}
func (f *fakeTTY) Write(p []byte) (int, error) { return f.out.Write(p) }
func (f *fakeTTY) Close() error                { f.closed = true; return nil }

func useTTY(t *testing.T, input string) *fakeTTY {
	t.Helper()
	tty := &fakeTTY{in: strings.NewReader(input)}
	oldOpen, oldStdio := OpenTTY, StdioIsTerminal
	OpenTTY = func() (io.ReadWriteCloser, error) { return tty, nil }
	StdioIsTerminal = func() bool { return true }
	t.Cleanup(func() { OpenTTY, StdioIsTerminal = oldOpen, oldStdio })
	return tty
}

func TestConfirmTerminalNoTTY(t *testing.T) {
	oldOpen, oldStdio := OpenTTY, StdioIsTerminal
	OpenTTY = func() (io.ReadWriteCloser, error) { return nil, errors.New("device not configured") }
	StdioIsTerminal = func() bool { return true }
	t.Cleanup(func() { OpenTTY, StdioIsTerminal = oldOpen, oldStdio })
	if err := Terminal("type it: ", "42"); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("got %v, want ErrNoTerminal", err)
	}
}

func TestConfirmPipedStdioRefused(t *testing.T) {
	tty := useTTY(t, "42\n")
	StdioIsTerminal = func() bool { return false } // stdin or stdout is a pipe or a file
	if err := Terminal("type it: ", "42"); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("got %v, want ErrNoTerminal", err)
	}
	if tty.read != 0 || tty.out.Len() != 0 {
		t.Error("the terminal was used although stdio is not a terminal")
	}
}

func TestConfirmMismatch(t *testing.T) {
	cases := map[string]string{
		"other":          "43\n",
		"trailing space": "1234 \n",
		"leading space":  " 1234\n",
		"tab":            "\t1234\n",
		"longer":         "12345\n",
		"no newline":     "1234",
		"eof":            "",
		"empty line":     "\n",
		"cr only":        "1234\r",
		"two cr":         "1234\r\r\n",
		"too long":       strings.Repeat("1", 100) + "\n",
		"long no nl":     strings.Repeat("1", 100),
		"nul":            "1234\x00\n",
	}
	for name, in := range cases {
		tty := useTTY(t, in)
		if err := Terminal("type it: ", "1234"); !errors.Is(err, ErrMismatch) {
			t.Errorf("%s: got %v, want ErrMismatch", name, err)
		}
		if !tty.closed {
			t.Errorf("%s: terminal not closed", name)
		}
	}
}

func TestConfirmReadsAtMost64Bytes(t *testing.T) {
	tty := useTTY(t, strings.Repeat("1", 1000))
	if err := Terminal("type it: ", "1234"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("got %v", err)
	}
	if tty.read > 64 {
		t.Errorf("read %d bytes", tty.read)
	}
}

func TestConfirmReadError(t *testing.T) {
	tty := useTTY(t, "")
	tty.in = errReader{}
	if err := Terminal("type it: ", "1234"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("got %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestConfirmEmptyExpectRefused(t *testing.T) {
	tty := useTTY(t, "\n")
	if err := Terminal("type it: ", ""); err == nil {
		t.Fatal("empty expect accepted")
	}
	if tty.read != 0 {
		t.Error("terminal read for an empty expect")
	}
}

func TestConfirmMatch(t *testing.T) {
	for _, in := range []string{"42\n", "42\r\n"} {
		tty := useTTY(t, in+"ignored\n")
		if err := Terminal("type it: ", "42"); err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got := tty.out.String(); got != "type it: " {
			t.Errorf("prompt written %q", got)
		}
		if !tty.closed {
			t.Error("terminal not closed")
		}
	}
}
