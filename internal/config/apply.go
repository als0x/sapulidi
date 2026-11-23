package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/state/atomic"
)

// Change is one setting an approved proposal wants. Key is "category.<name>.enabled", "category.<name>.age" or
// "rule.<pack>/<id>.enabled" or "schedule.interval"; Value is "true"/"false" or a duration like "30d"
// (for schedule.interval: 5m to 7d, see ParseScheduleInterval).
type Change struct {
	Key   string
	Value string
}

// neverCategories are never-bucket categories known without the registry (plan table "Categories").
var neverCategories = map[string]bool{"models": true}

var (
	catKey  = regexp.MustCompile(`^category\.([a-z0-9][a-z0-9-]*)\.(enabled|age)$`)
	ruleKey = regexp.MustCompile(`^rule\.([a-z0-9][a-z0-9_-]*/[a-z0-9][a-z0-9_.-]*)\.enabled$`)
)

// Apply records c in <config dir>/applied.toml. It never rewrites config.toml. The key and value are validated,
// and a never-bucket category cannot be named.
func Apply(p Paths, c Change) error {
	if c.Key == "schedule.interval" {
		d, err := ParseScheduleInterval(c.Value)
		if err != nil {
			return fmt.Errorf("config: %s: %w", c.Key, err)
		}
		return writeApplied(p, func(doc map[string]any) {
			sec, _ := doc["schedule"].(map[string]any)
			if sec == nil {
				sec = map[string]any{}
			}
			sec["interval"] = FormatInterval(d)
			doc["schedule"] = sec
		})
	}
	var section, name, field string
	if m := catKey.FindStringSubmatch(c.Key); m != nil {
		section, name, field = "category", m[1], m[2]
		if b, _ := bucketOf(nil, name); b == core.Never {
			return fmt.Errorf("config: category %q is never removed; it cannot be configured", name)
		}
	} else if m := ruleKey.FindStringSubmatch(c.Key); m != nil {
		section, name, field = "rule", m[1], "enabled"
	} else {
		return fmt.Errorf("config: unknown key %q", c.Key)
	}

	var val any
	if field == "age" {
		d, err := ParseDuration(c.Value)
		if err != nil {
			return fmt.Errorf("config: %s: %w", c.Key, err)
		}
		if d.D() < MinAgeAbsolute {
			return fmt.Errorf("config: %s: %s is below the minimum of 1h", c.Key, c.Value)
		}
		val = c.Value
	} else {
		if c.Value != "true" && c.Value != "false" {
			return fmt.Errorf("config: %s: value must be true or false, got %q", c.Key, c.Value)
		}
		val = c.Value == "true"
	}

	return writeApplied(p, func(doc map[string]any) {
		sec, _ := doc[section].(map[string]any)
		if sec == nil {
			sec = map[string]any{}
		}
		ent, _ := sec[name].(map[string]any)
		if ent == nil {
			ent = map[string]any{}
		}
		ent[field] = val
		sec[name] = ent
		doc[section] = sec
	})
}

// writeApplied reads applied.toml, lets set change it and writes it back atomically.
func writeApplied(p Paths, set func(doc map[string]any)) error {
	path := AppliedPath(p.Config)
	doc := map[string]any{}
	if _, err := toml.DecodeFile(path, &doc); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("config: %s: %w", path, err)
	}
	set(doc)

	var sb strings.Builder
	sb.WriteString("# Written by sapulidi from approved proposals. config.toml is never rewritten; this file wins per key.\n")
	if err := toml.NewEncoder(&sb).Encode(doc); err != nil {
		return err
	}
	return atomic.WriteFile(path, []byte(sb.String()))
}
