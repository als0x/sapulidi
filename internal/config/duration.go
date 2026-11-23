package config

import (
	"fmt"
	"strconv"
	"time"
)

// Duration is a positive span written as a number and one unit: m, h, d or w ("90m", "3h", "30d", "2w").
type Duration time.Duration

var units = map[byte]time.Duration{
	'm': time.Minute,
	'h': time.Hour,
	'd': 24 * time.Hour,
	'w': 7 * 24 * time.Hour,
}

// ParseDuration parses a Duration; zero, negative and unknown forms are errors.
func ParseDuration(s string) (Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	unit, ok := units[s[len(s)-1]]
	if !ok {
		return 0, fmt.Errorf("invalid duration %q: unit must be m, h, d or w", s)
	}
	n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid duration %q: need a positive whole number", s)
	}
	if n > int64(100*365*24*time.Hour/unit) {
		return 0, fmt.Errorf("invalid duration %q: too large", s)
	}
	return Duration(time.Duration(n) * unit), nil
}

func (d Duration) D() time.Duration { return time.Duration(d) }

// String writes the largest unit that divides the value exactly.
func (d Duration) String() string {
	v := time.Duration(d)
	for _, u := range []byte{'w', 'd', 'h', 'm'} {
		if v%units[u] == 0 && v > 0 {
			return strconv.FormatInt(int64(v/units[u]), 10) + string(u)
		}
	}
	return v.String()
}
