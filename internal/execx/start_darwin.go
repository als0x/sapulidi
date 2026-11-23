package execx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ProcessStart returns the start time of a process (ps -o lstart=).
func ProcessStart(pid int) (time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("process %d: %w", pid, err)
	}
	s := strings.Join(strings.Fields(string(out)), " ")
	if s == "" {
		return time.Time{}, fmt.Errorf("process %d not found", pid)
	}
	return time.ParseInLocation("Mon Jan 2 15:04:05 2006", s, time.Local)
}
