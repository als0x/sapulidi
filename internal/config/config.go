// Package config reads sapulidi's TOML config and the changes applied on top of it.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	SourceDefault = "default"
	SourceConfig  = "config.toml"
	SourceApplied = "applied.toml"

	// MinAgeAbsolute is the smallest age any config value may name (auto rules, spec 5.2).
	MinAgeAbsolute = time.Hour
	// MinAgeOptional is the smallest age for an optional category.
	MinAgeOptional = 24 * time.Hour
)

type CategoryConf struct {
	Enabled *bool
	Age     Duration // 0: not set
}

type RuleConf struct{ Enabled *bool }

type AgentConf struct {
	Enabled  bool
	CLI      string
	Interval Duration
	MaxUSD   float64
}

type NotifyConf struct{ Enabled bool }

// UpdateConf: Check allows asking whether a newer release exists; Auto lets the schedule install it.
type UpdateConf struct{ Check, Auto bool }

type Config struct {
	Roots           []string
	LowDiskGB       int
	GrowthGBPerWeek int
	Categories      map[string]CategoryConf
	Rules           map[string]RuleConf
	Agent           AgentConf
	Notify          NotifyConf
	Schedule        ScheduleConf
	Update          UpdateConf

	// Invalid lists config files that exist but are not valid TOML. NewSettings fails closed on such a config:
	// nothing is enabled and nothing is consented until the file is fixed.
	Invalid []string

	sources map[string]string // key (as in Change.Key, plus the plain keys) → source
}

// Defaults returns the config used when no file says otherwise.
func Defaults() *Config {
	return &Config{
		LowDiskGB:       15,
		GrowthGBPerWeek: 5,
		Categories:      map[string]CategoryConf{},
		Rules:           map[string]RuleConf{},
		Agent:           AgentConf{CLI: "", Interval: Duration(7 * 24 * time.Hour), MaxUSD: 0.25},
		Notify:          NotifyConf{Enabled: false},
		Schedule:        ScheduleConf{Interval: DefaultScheduleInterval},
		Update:          UpdateConf{Check: true},
		sources:         map[string]string{},
	}
}

// AppliedPath is the file Apply writes, next to the config file.
func AppliedPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "applied.toml")
}

// Load reads configPath, then applied.toml next to it; the later file wins per key. A missing file is
// not an error. Unknown or invalid values are ignored with a warning and keep their default. A file that is not
// valid TOML is listed in Config.Invalid with a warning (the other file is still read); only another I/O error
// is returned as err. The returned Config is never nil.
func Load(configPath string) (*Config, []string, error) {
	c := Defaults()
	var warns []string
	for _, l := range []struct{ path, source string }{
		{configPath, SourceConfig},
		{AppliedPath(configPath), SourceApplied},
	} {
		raw := map[string]any{}
		if _, err := toml.DecodeFile(l.path, &raw); err != nil {
			var pe toml.ParseError
			switch {
			case errors.Is(err, fs.ErrNotExist):
			case errors.As(err, &pe):
				c.Invalid = append(c.Invalid, l.path)
				warns = append(warns, fmt.Sprintf("%s is not valid TOML and is ignored; nothing will run until it is fixed: %v", l.path, err))
			default:
				return c, warns, fmt.Errorf("config: %s: %w", l.path, err)
			}
			continue
		}
		warns = append(warns, c.merge(raw, l.source)...)
	}
	return c, warns, nil
}

// merge applies one decoded file. Every problem becomes a warning; the offending key is skipped.
func (c *Config) merge(raw map[string]any, src string) []string {
	var w []string
	warn := func(f string, a ...any) { w = append(w, fmt.Sprintf(f+" (%s)", append(a, src)...)) }

	asInt := func(key string, v any) (int, bool) {
		n, ok := v.(int64)
		if !ok || n <= 0 || n > 1<<20 {
			warn("%s must be a positive whole number; ignored", key)
			return 0, false
		}
		return int(n), true
	}
	asBool := func(key string, v any) (bool, bool) {
		b, ok := v.(bool)
		if !ok {
			warn("%s must be true or false; ignored", key)
		}
		return b, ok
	}
	asAge := func(key string, v any) (Duration, bool) {
		s, ok := v.(string)
		if !ok {
			warn("%s must be a string like \"30d\"; ignored", key)
			return 0, false
		}
		d, err := ParseDuration(s)
		if err != nil {
			warn("%s: %v; ignored", key, err)
			return 0, false
		}
		if d.D() < MinAgeAbsolute {
			warn("%s: %s is below the minimum of 1h; default kept", key, s)
			return 0, false
		}
		return d, true
	}
	table := func(key string, v any) (map[string]any, bool) {
		m, ok := v.(map[string]any)
		if !ok {
			warn("%s must be a table; ignored", key)
		}
		return m, ok
	}
	unknown := func(key string) { warn("unknown key %q ignored", key) }

	for _, k := range sortedKeys(raw) {
		v := raw[k]
		switch k {
		case "roots":
			list, ok := v.([]any)
			if !ok {
				warn("roots must be a list of absolute paths; ignored")
				continue
			}
			var roots []string
			for _, e := range list {
				s, ok := e.(string)
				if !ok || !filepath.IsAbs(s) {
					warn("root %v is not an absolute path; dropped", e)
					continue
				}
				roots = append(roots, filepath.Clean(s))
			}
			c.Roots = roots
			c.sources["roots"] = src
		case "low_disk_gb":
			if n, ok := asInt(k, v); ok {
				c.LowDiskGB, c.sources[k] = n, src
			}
		case "growth_gb_per_week":
			if n, ok := asInt(k, v); ok {
				c.GrowthGBPerWeek, c.sources[k] = n, src
			}
		case "category":
			t, ok := table(k, v)
			if !ok {
				continue
			}
			for _, name := range sortedKeys(t) {
				ct, ok := table("category."+name, t[name])
				if !ok {
					continue
				}
				cc := c.Categories[name]
				for _, f := range sortedKeys(ct) {
					key := "category." + name + "." + f
					switch f {
					case "enabled":
						if b, ok := asBool(key, ct[f]); ok {
							cc.Enabled, c.sources[key] = &b, src
						}
					case "age":
						if d, ok := asAge(key, ct[f]); ok {
							cc.Age, c.sources[key] = d, src
						}
					default:
						unknown(key)
					}
				}
				c.Categories[name] = cc
			}
		case "rule":
			t, ok := table(k, v)
			if !ok {
				continue
			}
			for _, name := range sortedKeys(t) {
				rt, ok := table("rule."+name, t[name])
				if !ok {
					continue
				}
				rc := c.Rules[name]
				for _, f := range sortedKeys(rt) {
					key := "rule." + name + "." + f
					if f != "enabled" {
						unknown(key)
						continue
					}
					if b, ok := asBool(key, rt[f]); ok {
						rc.Enabled, c.sources[key] = &b, src
					}
				}
				c.Rules[name] = rc
			}
		case "agent":
			t, ok := table(k, v)
			if !ok {
				continue
			}
			for _, f := range sortedKeys(t) {
				key := "agent." + f
				switch f {
				case "enabled":
					if b, ok := asBool(key, t[f]); ok {
						c.Agent.Enabled, c.sources[key] = b, src
					}
				case "cli":
					if s, ok := t[f].(string); ok {
						c.Agent.CLI, c.sources[key] = s, src
					} else {
						warn("%s must be a string; ignored", key)
					}
				case "interval":
					if d, ok := asAge(key, t[f]); ok {
						c.Agent.Interval, c.sources[key] = d, src
					}
				case "max_usd":
					var x float64
					switch n := t[f].(type) {
					case float64:
						x = n
					case int64:
						x = float64(n)
					}
					if x <= 0 {
						warn("%s must be a positive number; ignored", key)
						continue
					}
					c.Agent.MaxUSD, c.sources[key] = x, src
				default:
					unknown(key)
				}
			}
		case "notify":
			t, ok := table(k, v)
			if !ok {
				continue
			}
			for _, f := range sortedKeys(t) {
				key := "notify." + f
				if f != "enabled" {
					unknown(key)
					continue
				}
				if b, ok := asBool(key, t[f]); ok {
					c.Notify.Enabled, c.sources[key] = b, src
				}
			}
		case "update":
			t, ok := table(k, v)
			if !ok {
				continue
			}
			for _, f := range sortedKeys(t) {
				key := "update." + f
				var dst *bool
				switch f {
				case "check":
					dst = &c.Update.Check
				case "auto":
					dst = &c.Update.Auto
				default:
					unknown(key)
					continue
				}
				if b, ok := asBool(key, t[f]); ok {
					*dst, c.sources[key] = b, src
				}
			}
		case "schedule":
			t, ok := table(k, v)
			if !ok {
				continue
			}
			for _, f := range sortedKeys(t) {
				key := "schedule." + f
				if f != "interval" {
					unknown(key)
					continue
				}
				s, ok := t[f].(string)
				if !ok {
					warn("%s must be a string like \"30m\"; ignored", key)
					continue
				}
				d, err := ParseScheduleInterval(s)
				if err != nil {
					warn("%s: %v; default kept", key, err)
					continue
				}
				c.Schedule.Interval, c.sources[key] = d, src
			}
		default:
			unknown(k)
		}
	}
	return w
}

// Value is one effective setting and where it came from.
type Value struct {
	Key, Value, Source string
}

// Effective lists every setting with its value and source (default, config.toml or applied.toml), sorted by key.
func (c *Config) Effective() []Value {
	src := func(k string) string {
		if s, ok := c.sources[k]; ok {
			return s
		}
		return SourceDefault
	}
	vals := []Value{
		{"roots", strings.Join(c.Roots, ","), src("roots")},
		{"low_disk_gb", strconv.Itoa(c.LowDiskGB), src("low_disk_gb")},
		{"growth_gb_per_week", strconv.Itoa(c.GrowthGBPerWeek), src("growth_gb_per_week")},
		{"agent.enabled", strconv.FormatBool(c.Agent.Enabled), src("agent.enabled")},
		{"agent.cli", c.Agent.CLI, src("agent.cli")},
		{"agent.interval", c.Agent.Interval.String(), src("agent.interval")},
		{"agent.max_usd", strconv.FormatFloat(c.Agent.MaxUSD, 'f', -1, 64), src("agent.max_usd")},
		{"notify.enabled", strconv.FormatBool(c.Notify.Enabled), src("notify.enabled")},
		{"schedule.interval", FormatInterval(c.Schedule.Interval), src("schedule.interval")},
		{"update.auto", strconv.FormatBool(c.Update.Auto), src("update.auto")},
		{"update.check", strconv.FormatBool(c.Update.Check), src("update.check")},
	}
	for n, cc := range c.Categories {
		if cc.Enabled != nil {
			k := "category." + n + ".enabled"
			vals = append(vals, Value{k, strconv.FormatBool(*cc.Enabled), src(k)})
		}
		if cc.Age != 0 {
			k := "category." + n + ".age"
			vals = append(vals, Value{k, cc.Age.String(), src(k)})
		}
	}
	for n, rc := range c.Rules {
		if rc.Enabled != nil {
			k := "rule." + n + ".enabled"
			vals = append(vals, Value{k, strconv.FormatBool(*rc.Enabled), src(k)})
		}
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i].Key < vals[j].Key })
	return vals
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
