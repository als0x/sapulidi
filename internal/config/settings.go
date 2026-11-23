package config

import (
	"fmt"
	"sort"
	"time"

	"github.com/als0x/sapulidi/internal/core"
)

// Consent is the record `setup` writes (spec S2): when, and the rule keys (with revision) the user confirmed.
// state.Consent is an alias of this type, so config does not import state.
type Consent struct {
	At    time.Time
	Rules []string
}

type settings struct {
	c       *Config
	consent map[string]bool
	buckets map[string]core.Bucket
}

// NewSettings builds core.Settings from the config, the consent record (nil: nothing consented) and the bucket
// of every category.
func NewSettings(c *Config, consent *Consent, buckets map[string]core.Bucket) core.Settings {
	if c == nil {
		c = Defaults()
	}
	s := &settings{c: c, consent: map[string]bool{}, buckets: buckets}
	if consent != nil {
		for _, r := range consent.Rules {
			s.consent[r] = true
		}
	}
	return s
}

// minAge is the smallest age a category accepts: auto 1 h, optional (and unknown) 24 h.
func minAge(b core.Bucket, known bool) time.Duration {
	if known && b == core.Auto {
		return MinAgeAbsolute
	}
	return MinAgeOptional
}

// bucketOf finds a category's bucket: the map passed in, then the registry, then the never list known
// without a registry. known is false for a category nobody declared.
func bucketOf(buckets map[string]core.Bucket, category string) (b core.Bucket, known bool) {
	if neverCategories[category] {
		return core.Never, true
	}
	if b, ok := buckets[category]; ok {
		return b, true
	}
	return core.CategoryBucket(category)
}

func (s *settings) CategoryEnabled(category string) bool {
	if len(s.c.Invalid) > 0 {
		return false // a corrupt file may have held enabled=false: run nothing
	}
	b, known := bucketOf(s.buckets, category)
	if known && b == core.Never {
		return false
	}
	en := s.c.Categories[category].Enabled
	if known && b == core.Auto {
		return en == nil || *en
	}
	return en != nil && *en // optional, and any category the caller did not declare
}

func (s *settings) RuleEnabled(ruleKey string) bool {
	id := ruleKey
	for i := 0; i < len(ruleKey); i++ {
		if ruleKey[i] == '@' {
			id = ruleKey[:i]
			break
		}
	}
	en := s.c.Rules[id].Enabled
	return en == nil || *en
}

func (s *settings) Age(category string, def time.Duration) time.Duration {
	a := s.c.Categories[category].Age
	if a == 0 {
		return def
	}
	b, known := bucketOf(s.buckets, category)
	if known && b == core.Never {
		return def
	}
	if a.D() < minAge(b, known) {
		return def
	}
	return a.D()
}

func (s *settings) Consented(ruleKey string) bool {
	return len(s.c.Invalid) == 0 && s.consent[ruleKey] // a corrupt config means no consent is trusted
}
func (s *settings) LowDiskBytes() uint64      { return uint64(s.c.LowDiskGB) << 30 }
func (s *settings) GrowthBytesPerWeek() int64 { return int64(s.c.GrowthGBPerWeek) << 30 }
func (s *settings) NotifyEnabled() bool       { return s.c.Notify.Enabled }

// AgeWarnings names every configured age that NewSettings will replace by the default because it is below the
// minimum of its category's bucket (Load already drops values below 1 h).
func (c *Config) AgeWarnings(buckets map[string]core.Bucket) []string {
	var w []string
	names := make([]string, 0, len(c.Categories))
	for n := range c.Categories {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a := c.Categories[n].Age
		b, known := bucketOf(buckets, n)
		if a != 0 && a.D() < minAge(b, known) {
			w = append(w, fmt.Sprintf("category.%s.age: %s is below the minimum of %s; default kept", n, a, Duration(minAge(b, known))))
		}
	}
	return w
}
