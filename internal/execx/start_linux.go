package execx

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const clockTicks = 100 // USER_HZ is 100 on every Linux target we build for

// ProcessStart returns the start time of a process from /proc/<pid>/stat field 22 plus boot time.
func ProcessStart(pid int) (time.Time, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return time.Time{}, fmt.Errorf("process %d: %w", pid, err)
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')') // the command name may contain spaces and parentheses
	if i < 0 {
		return time.Time{}, fmt.Errorf("process %d: bad stat", pid)
	}
	f := strings.Fields(s[i+1:]) // f[0] is field 3
	if len(f) < 20 {
		return time.Time{}, fmt.Errorf("process %d: short stat", pid)
	}
	ticks, err := strconv.ParseInt(f[19], 10, 64) // field 22
	if err != nil {
		return time.Time{}, err
	}
	st, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	for _, line := range strings.Split(string(st), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			boot, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return time.Time{}, err
			}
			return time.Unix(boot+ticks/clockTicks, (ticks%clockTicks)*int64(time.Second/clockTicks)), nil
		}
	}
	return time.Time{}, fmt.Errorf("btime missing in /proc/stat")
}
