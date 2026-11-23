package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultScheduleInterval is how often the scheduled run starts when nothing says otherwise.
	DefaultScheduleInterval = 30 * time.Minute
	// MinScheduleInterval and MaxScheduleInterval bound schedule.interval.
	MinScheduleInterval = 5 * time.Minute
	MaxScheduleInterval = 7 * 24 * time.Hour
)

// ScheduleConf is the [schedule] table.
type ScheduleConf struct{ Interval time.Duration }

const intervalHint = "write 30m, 2h or 1d"

var intervalSyntax = regexp.MustCompile(`^([0-9]+)([smhd])$`)

// ParseScheduleInterval reads <n>[s|m|h|d] (n a positive whole number) as a duration that is a whole number of
// minutes between MinScheduleInterval and MaxScheduleInterval. Every refusal names its reason. It does not
// know which scheduler backend runs the job; a backend that cannot express the value refuses it at install time.
func ParseScheduleInterval(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("interval is empty: %s", intervalHint)
	}
	if _, err := strconv.ParseUint(s, 10, 64); err == nil {
		return 0, fmt.Errorf("interval %q has no unit: %s", s, intervalHint)
	}
	m := intervalSyntax.FindStringSubmatch(s)
	n, err := uint64(0), error(nil)
	if m != nil {
		n, err = strconv.ParseUint(m[1], 10, 64)
	}
	if m == nil || (err == nil && n == 0) {
		return 0, fmt.Errorf("interval %q is not <n>[s|m|h|d] with n a positive whole number: %s", s, intervalHint)
	}
	unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
	if err != nil || n > uint64(MaxScheduleInterval/unit) {
		return 0, fmt.Errorf("interval %s is above the maximum of 7d", s)
	}
	d := time.Duration(n) * unit
	switch {
	case d < MinScheduleInterval:
		return 0, fmt.Errorf("interval %s is below the minimum of 5m: a run takes seconds, cron cannot go below a minute "+
			"and auto rules only apply after an hour", s)
	case d%time.Minute != 0:
		return 0, fmt.Errorf("interval %s is not a whole number of minutes: cron cannot express seconds", s)
	}
	return d, nil
}

// FormatInterval writes d as the shortest whole unit among d, h and m: 5m, 90m, 2h, 1d.
func FormatInterval(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return strconv.FormatInt(int64(d/(24*time.Hour)), 10) + "d"
	case d%time.Hour == 0:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	}
	return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
}

// AppliedScheduleInterval is the schedule.interval stored in applied.toml and whether there is one. An applied.toml
// that cannot be read or parsed is an error.
func AppliedScheduleInterval(p Paths) (string, bool, error) {
	doc := map[string]any{}
	if _, err := toml.DecodeFile(AppliedPath(p.Config), &doc); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("config: %s: %w", AppliedPath(p.Config), err)
	}
	sec, _ := doc["schedule"].(map[string]any)
	s, ok := sec["interval"].(string)
	return s, ok, nil
}

// RestoreScheduleInterval puts schedule.interval in applied.toml back to what AppliedScheduleInterval returned:
// the stored value when had, no value at all otherwise.
func RestoreScheduleInterval(p Paths, prev string, had bool) error {
	if had {
		return Apply(p, Change{Key: "schedule.interval", Value: prev})
	}
	return writeApplied(p, func(doc map[string]any) {
		if sec, ok := doc["schedule"].(map[string]any); ok {
			delete(sec, "interval")
			if len(sec) == 0 {
				delete(doc, "schedule")
			}
		}
	})
}

// ScheduleInterval is the effective schedule.interval of the config at p. A Paths without a config file path
// (a fixture) and any unreadable config give the default.
func ScheduleInterval(p Paths) time.Duration {
	if p.Config == "" {
		return DefaultScheduleInterval
	}
	c, _, _ := Load(p.Config)
	return c.Schedule.Interval
}
