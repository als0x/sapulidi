// Package execx runs external commands for the engine and reads process facts.
package execx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const defaultTimeout = 30 * time.Second

// ExitError is returned when a command exits non-zero.
type ExitError struct {
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit status %d: %s", e.Code, e.Stderr)
}

// Real implements core.Runner with os/exec.
type Real struct{}

func (Real) Run(ctx context.Context, dir string, argv ...string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("execx: empty argv")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = childEnv(argv[0], os.Environ())
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer devnull.Close()
	cmd.Stdin = devnull
	// Own process group, so a timeout kills grandchildren too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// A child that inherits the pipes must not keep Run waiting after the kill.
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return stdout.Bytes(), fmt.Errorf("%s: %w", argv[0], ctx.Err())
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return stdout.Bytes(), &ExitError{Code: ee.ExitCode(), Stderr: stderr.String()}
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

func (Real) LookPath(name string) (string, error) { return exec.LookPath(name) }
