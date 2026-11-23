package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseScheduleIntervalTable(t *testing.T) {
	ok := map[string]time.Duration{
		"5m":   5 * time.Minute,
		"300s": 5 * time.Minute,
		"90m":  90 * time.Minute,
		"2h":   2 * time.Hour,
		"1d":   24 * time.Hour,
		"7d":   7 * 24 * time.Hour,
	}
	for in, want := range ok {
		got, err := ParseScheduleInterval(in)
		if err != nil || got != want {
			t.Errorf("%q: got %v, %v; want %v", in, got, err, want)
		}
	}
	bad := map[string]string{
		"4m":   "below the minimum",
		"30s":  "below the minimum",
		"8d":   "above the maximum",
		"0m":   "positive whole number",
		"-5m":  "positive whole number",
		"5":    "write 30m, 2h or 1d",
		"5x":   "positive whole number",
		"1.5h": "positive whole number",
		"330s": "whole number of minutes",
		"":     "write 30m, 2h or 1d",
	}
	for in, reason := range bad {
		_, err := ParseScheduleInterval(in)
		if err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("%q: error %v, want it to say %q", in, err, reason)
		}
	}
}

func TestFormatInterval(t *testing.T) {
	for d, want := range map[time.Duration]string{
		5 * time.Minute: "5m", 90 * time.Minute: "90m", 2 * time.Hour: "2h", 24 * time.Hour: "1d", 7 * 24 * time.Hour: "7d",
		36 * time.Hour: "36h",
	} {
		if got := FormatInterval(d); got != want {
			t.Errorf("%v: got %q want %q", d, got, want)
		}
	}
}

func scheduleConfigPath(t *testing.T) Paths {
	return Paths{Config: filepath.Join(t.TempDir(), "config.toml")}
}

func TestScheduleIntervalDefault(t *testing.T) {
	p := scheduleConfigPath(t)
	c, warns, err := Load(p.Config)
	if err != nil || len(warns) != 0 {
		t.Fatalf("%v %v", err, warns)
	}
	if c.Schedule.Interval != 30*time.Minute || DefaultScheduleInterval != 30*time.Minute {
		t.Fatalf("default %v", c.Schedule.Interval)
	}
	if v := effectiveValue(t, c, "schedule.interval"); v.Value != "30m" || v.Source != SourceDefault {
		t.Fatalf("%+v", v)
	}
}

func effectiveValue(t *testing.T, c *Config, key string) Value {
	t.Helper()
	for _, v := range c.Effective() {
		if v.Key == key {
			return v
		}
	}
	t.Fatalf("no %s in Effective", key)
	return Value{}
}

func TestScheduleIntervalRoundTripThroughApplied(t *testing.T) {
	p := scheduleConfigPath(t)
	if err := Apply(p, Change{Key: "schedule.interval", Value: "300s"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(AppliedPath(p.Config))
	if err != nil || !strings.Contains(string(body), `interval = "5m"`) {
		t.Fatalf("applied.toml stores the canonical form: %v\n%s", err, body)
	}
	c, warns, err := Load(p.Config)
	if err != nil || len(warns) != 0 {
		t.Fatalf("%v %v", err, warns)
	}
	if c.Schedule.Interval != 5*time.Minute {
		t.Fatalf("got %v", c.Schedule.Interval)
	}
	if v := effectiveValue(t, c, "schedule.interval"); v.Value != "5m" || v.Source != SourceApplied {
		t.Fatalf("%+v", v)
	}
	// Other applied keys survive a second write.
	if err := Apply(p, Change{Key: "category.deps.enabled", Value: "false"}); err != nil {
		t.Fatal(err)
	}
	if err := Apply(p, Change{Key: "schedule.interval", Value: "2h"}); err != nil {
		t.Fatal(err)
	}
	c, _, _ = Load(p.Config)
	if c.Schedule.Interval != 2*time.Hour || c.Categories["deps"].Enabled == nil {
		t.Fatalf("%v %+v", c.Schedule.Interval, c.Categories["deps"])
	}
}

func TestApplyRejectsBadScheduleInterval(t *testing.T) {
	p := scheduleConfigPath(t)
	if err := Apply(p, Change{Key: "schedule.interval", Value: "4m"}); err == nil {
		t.Fatal("4m accepted")
	}
	if _, err := os.Stat(AppliedPath(p.Config)); err == nil {
		t.Fatal("a refused value wrote applied.toml")
	}
}

func TestScheduleIntervalFromConfigTomlAndBadValue(t *testing.T) {
	p := scheduleConfigPath(t)
	if err := os.WriteFile(p.Config, []byte("[schedule]\ninterval = \"1d\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, warns, _ := Load(p.Config)
	if c.Schedule.Interval != 24*time.Hour || len(warns) != 0 {
		t.Fatalf("%v %v", c.Schedule.Interval, warns)
	}
	if err := os.WriteFile(p.Config, []byte("[schedule]\ninterval = \"30s\"\nbogus = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, warns, _ = Load(p.Config)
	if c.Schedule.Interval != 30*time.Minute || len(warns) != 2 {
		t.Fatalf("%v %v", c.Schedule.Interval, warns)
	}
}
