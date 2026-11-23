// Package confirm is the terminal tier (spec S15): an action the user must type in a real terminal.
package confirm

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var (
	// ErrNoTerminal means there is no real terminal: /dev/tty cannot be opened (agent Bash tool, hooks,
	// cron; spike SP7) or stdin or stdout is not a terminal (a pipe or a redirect).
	ErrNoTerminal = errors.New("no terminal available")
	// ErrMismatch means the user typed something other than the expected text.
	ErrMismatch = errors.New("confirmation did not match")
)

// maxLine is the most bytes read from the terminal, newline included.
const maxLine = 64

// OpenTTY opens the controlling terminal for reading and writing. Opening is the test: permission bits
// on /dev/tty say nothing (SP7). Tests replace it and never touch the real terminal.
var OpenTTY = func() (io.ReadWriteCloser, error) {
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}

// StdioIsTerminal reports whether both os.Stdin and os.Stdout are terminals. Tests replace it.
var StdioIsTerminal = func() bool { return isTerminal(os.Stdin.Fd()) && isTerminal(os.Stdout.Fd()) }

// Terminal prints prompt on the terminal, reads one line and returns nil only when the line, without its
// trailing "\n" or "\r\n" and nothing else removed, equals expect exactly. At most 64 bytes are read; a
// line that is longer, has no newline, or fails to read is ErrMismatch. An empty expect is refused.
//
// Limit: a pty driven by the agent (script, expect, python pty) gives it a real terminal, and that cannot
// be told apart in code. This tier stops pipes, stdin redirects, hooks and plain agent shells only.
func Terminal(prompt, expect string) error {
	if expect == "" {
		return errors.New("confirm: empty expected text")
	}
	if !StdioIsTerminal() {
		return fmt.Errorf("%w: stdin and stdout are not both terminals", ErrNoTerminal)
	}
	tty, err := OpenTTY()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoTerminal, err)
	}
	defer tty.Close()
	if _, err := io.WriteString(tty, prompt); err != nil {
		return fmt.Errorf("%w: %v", ErrNoTerminal, err)
	}
	line := make([]byte, 0, maxLine)
	var b [1]byte
	for len(line) < maxLine {
		n, err := tty.Read(b[:])
		if n > 0 {
			line = append(line, b[0])
			if b[0] == '\n' {
				break
			}
		}
		if err != nil {
			break
		}
	}
	s := string(line)
	if !strings.HasSuffix(s, "\n") {
		return ErrMismatch
	}
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	if s != expect {
		return ErrMismatch
	}
	return nil
}
