package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
)

// MaxOutput is the most stdout the agent CLI may write; a run that writes more fails.
const MaxOutput = 1 << 20

// ErrOutputTooLarge means the child wrote more than MaxOutput bytes to stdout.
var ErrOutputTooLarge = errors.New("agent output exceeded 1 MiB")

// childEnvNames are the only variables the agent CLI receives. The agent runs with the user's login and money,
// so nothing else of the environment (API keys, cloud and forge credentials, tokens, secrets) reaches it, and no
// name is matched by pattern: a variable not listed here is dropped.
var childEnvNames = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "LANG": true, "LC_ALL": true, "LC_CTYPE": true,
	"TMPDIR": true, "SHELL": true, "TERM": true,
	"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true, "XDG_CACHE_HOME": true,
	"CLAUDE_CONFIG_DIR": true,
}

// ChildEnv keeps, in order, the entries of environ whose name is in the allowlist.
func ChildEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if ok && childEnvNames[name] {
			out = append(out, kv)
		}
	}
	return out
}

// NewRunner returns the runner used for the agent CLI: the child gets ChildEnv(environ) only, no stdin, its own
// process group (killed on timeout) and at most MaxOutput bytes of stdout.
func NewRunner(environ []string) core.Runner { return &childRunner{env: ChildEnv(environ)} }

type childRunner struct{ env []string }

func (r *childRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

// limitedWriter stores up to limit bytes; the first byte over the limit fails the write and calls onExceed.
type limitedWriter struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
	onExceed func()
	discard  bool // drop what does not fit instead of failing (stderr)
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.discard {
		if room := w.limit - w.buf.Len(); room > 0 {
			w.buf.Write(p[:min(room, len(p))])
		}
		return len(p), nil
	}
	if w.exceeded || w.buf.Len()+len(p) > w.limit {
		if !w.exceeded {
			w.exceeded = true
			if w.onExceed != nil {
				w.onExceed()
			}
		}
		return 0, ErrOutputTooLarge
	}
	return w.buf.Write(p)
}

func (r *childRunner) Run(ctx context.Context, dir string, argv ...string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("agent: empty argv")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append([]string(nil), r.env...)
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer devnull.Close()
	cmd.Stdin = devnull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	stdout := &limitedWriter{limit: MaxOutput, onExceed: cancel}
	stderr := &limitedWriter{limit: 64 << 10, discard: true}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	if stdout.exceeded {
		return nil, ErrOutputTooLarge
	}
	if err != nil {
		if ctx.Err() != nil {
			return stdout.buf.Bytes(), fmt.Errorf("%s: %w", argv[0], ctx.Err())
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return stdout.buf.Bytes(), &execx.ExitError{Code: ee.ExitCode(), Stderr: stderr.buf.String()}
		}
		return stdout.buf.Bytes(), err
	}
	return stdout.buf.Bytes(), nil
}
