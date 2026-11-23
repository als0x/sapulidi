package execx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRealRunnerExitError(t *testing.T) {
	_, err := Real{}.Run(context.Background(), "", "sh", "-c", "echo x >&2; exit 3")
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("want *ExitError, got %v", err)
	}
	if ee.Code != 3 || ee.Stderr != "x\n" {
		t.Fatalf("got %+v", ee)
	}
}

func TestRealRunnerTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	// The grandchild sleeps for a duration unique to this process, so ps can find it afterwards.
	_, err := Real{}.Run(ctx, "", "sh", "-c", "sleep 5."+fmt.Sprint(os.Getpid())+" & wait")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("did not time out")
	}
	time.Sleep(200 * time.Millisecond)
	out, _ := exec.Command("ps", "-axo", "args=").Output()
	if strings.Contains(string(out), "sleep 5."+fmt.Sprint(os.Getpid())) {
		t.Fatal("grandchild survived the timeout")
	}
}

func TestRealRunnerStdout(t *testing.T) {
	out, err := Real{}.Run(context.Background(), "", "sh", "-c", "cat; echo hi")
	if err != nil || string(out) != "hi\n" {
		t.Fatalf("out=%q err=%v (stdin must be /dev/null)", out, err)
	}
}

func TestProcessStartSelf(t *testing.T) {
	st, err := ProcessStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if st.IsZero() || st.After(time.Now().Add(time.Minute)) || time.Since(st) > 24*time.Hour {
		t.Fatalf("implausible start %v", st)
	}
}

func TestProcessStartMissingPid(t *testing.T) {
	if _, err := ProcessStart(1 << 30); err == nil {
		t.Fatal("want error for missing pid")
	}
}
