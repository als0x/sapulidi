// Package agent runs the opt-in scheduled agent (spec 8) and handles its proposals. The agent has no Write and no
// delete tool: it returns proposals as data, every one is validated here, and only an explicit user command
// applies one, through the engine and the config layer (spec S1, S14).
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing/fstest"

	"github.com/BurntSushi/toml"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	packsload "github.com/als0x/sapulidi/internal/packs"
)

// Proposal kinds.
const (
	KindEnable    = "enable-category"
	KindDisable   = "disable-category"
	KindSetAge    = "set-age"
	KindAddRule   = "add-rule"
	KindCleanOnce = "clean-once"
)

// Proposal is one suggestion of the agent. Every field is data: it is stored and shown, never executed.
// For add-rule, Target is the rule id and Value is the rule as TOML keys (id, category, bucket, kind, names,
// markers, min_age, os); for set-age, Value is the age; clean-once lists Paths of the optional category Target.
type Proposal struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Target   string   `json:"target"`
	Value    string   `json:"value,omitempty"`
	Paths    []string `json:"paths,omitempty"`
	Reason   string   `json:"reason"`
	EstBytes int64    `json:"est_bytes"`
	Evidence []string `json:"evidence,omitempty"`
}

const (
	maxReason   = 600
	maxEvidence = 10
	maxEvLine   = 400
	maxValue    = 2000
	maxPaths    = 50
	maxPathLen  = 4096
)

// checkSizes enforces the length limit of every field. It runs while a proposal is decoded (UnmarshalJSON) and
// again in Validate.
func checkSizes(p Proposal) error {
	switch {
	case len(p.ID) > 64 || len(p.Kind) > 32 || len(p.Target) > 256:
		return fmt.Errorf("id, kind or target is too long")
	case len(p.Reason) > maxReason:
		return fmt.Errorf("reason is longer than %d bytes", maxReason)
	case len(p.Value) > maxValue:
		return fmt.Errorf("value is longer than %d bytes", maxValue)
	case len(p.Evidence) > maxEvidence:
		return fmt.Errorf("at most %d evidence lines", maxEvidence)
	case len(p.Paths) > maxPaths:
		return fmt.Errorf("at most %d paths", maxPaths)
	}
	for _, e := range p.Evidence {
		if len(e) > maxEvLine {
			return fmt.Errorf("an evidence line is longer than %d bytes", maxEvLine)
		}
	}
	for _, x := range p.Paths {
		if len(x) > maxPathLen {
			return fmt.Errorf("a path is longer than %d bytes", maxPathLen)
		}
	}
	return nil
}

// UnmarshalJSON decodes a proposal and refuses one with an over-long field.
func (p *Proposal) UnmarshalJSON(b []byte) error {
	type plain Proposal
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	if err := checkSizes(Proposal(v)); err != nil {
		return err
	}
	*p = Proposal(v)
	return nil
}

// idRe keeps ids usable as file names and as the tail of a pack name (the pack validator allows 41 characters).
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidID reports whether id can name a proposal file.
func ValidID(id string) bool { return idRe.MatchString(id) }

// Validate checks a proposal against spec S14. It does not trust the agent: a known kind, a category of the
// right bucket (never categories are refused), an age above the minimum, a rule that passes the pack validator,
// and clean-once paths that exist. It is run again at apply time.
func Validate(p Proposal) error {
	if !idRe.MatchString(p.ID) {
		return fmt.Errorf("proposal id %q is not valid", p.ID)
	}
	if strings.TrimSpace(p.Reason) == "" {
		return fmt.Errorf("reason is empty")
	}
	if err := checkSizes(p); err != nil {
		return err
	}
	if p.EstBytes < 0 {
		return fmt.Errorf("est_bytes is negative")
	}
	switch p.Kind {
	case KindEnable, KindDisable:
		return optionalCategory(p.Target)
	case KindSetAge:
		if err := optionalCategory(p.Target); err != nil {
			return err
		}
		d, err := config.ParseDuration(p.Value)
		if err != nil {
			return fmt.Errorf("age %q: %w", p.Value, err)
		}
		if d.D() < config.MinAgeOptional {
			return fmt.Errorf("age %s is below the minimum of %s", p.Value, config.MinAgeOptional)
		}
		return nil
	case KindCleanOnce:
		if err := optionalCategory(p.Target); err != nil {
			return err
		}
		return validPaths(p.Paths)
	case KindAddRule:
		_, err := RuleFile(p)
		return err
	}
	return fmt.Errorf("unknown kind %q", p.Kind)
}

// optionalCategory accepts only a registered category of the optional bucket.
func optionalCategory(name string) error {
	b, ok := core.CategoryBucket(name)
	switch {
	case !ok:
		return fmt.Errorf("unknown category %q", name)
	case b == core.Never:
		return fmt.Errorf("category %q is never removed", name)
	case b != core.Optional:
		return fmt.Errorf("category %q is not optional", name)
	}
	return nil
}

func validPaths(paths []string) error {
	if len(paths) == 0 || len(paths) > maxPaths {
		return fmt.Errorf("clean-once needs 1 to %d paths", maxPaths)
	}
	for _, p := range paths {
		if len(p) > maxPathLen || !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return fmt.Errorf("path %q is not an absolute, clean path", p)
		}
		if _, err := os.Lstat(p); err != nil {
			return fmt.Errorf("path %q does not exist", p)
		}
	}
	return nil
}

// ruleDoc is the rule an add-rule proposal carries. Unknown keys are errors.
type ruleDoc struct {
	ID       string   `toml:"id"`
	Category string   `toml:"category"`
	Bucket   string   `toml:"bucket"`
	Kind     string   `toml:"kind"`
	Names    []string `toml:"names"`
	Markers  []string `toml:"markers"`
	MinAge   string   `toml:"min_age"`
	OS       []string `toml:"os"`
}

// PackName is the user pack an add-rule proposal is written to.
func PackName(id string) string { return "proposed-" + id }

// RuleFile validates an add-rule proposal and returns the text of the user pack file it becomes. The pack is
// rendered from the decoded fields (nothing from the agent is copied verbatim) and loaded through the same
// validator as every user pack; only an optional rule of an optional category is accepted.
func RuleFile(p Proposal) ([]byte, error) {
	if p.Kind != KindAddRule {
		return nil, fmt.Errorf("not an add-rule proposal")
	}
	var d ruleDoc
	md, err := toml.Decode(p.Value, &d)
	if err != nil {
		return nil, fmt.Errorf("rule: %w", err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return nil, fmt.Errorf("rule: unknown key %q", u[0].String())
	}
	if d.ID == "" || p.Target != d.ID {
		return nil, fmt.Errorf("rule: the target %q must be the rule id %q", p.Target, d.ID)
	}
	if err := optionalCategory(d.Category); err != nil {
		return nil, fmt.Errorf("rule: %w", err)
	}
	if d.Bucket != string(core.Optional) {
		return nil, fmt.Errorf("rule: a proposed rule must be in the optional bucket")
	}
	if d.MinAge == "" {
		return nil, fmt.Errorf("rule: min_age is required")
	}
	text, err := renderPack(PackName(p.ID), d)
	if err != nil {
		return nil, err
	}
	name := PackName(p.ID)
	rules, errs := packsload.Load(fstest.MapFS{name + ".toml": {Data: text}}, "")
	if len(errs) > 0 {
		return nil, fmt.Errorf("rule: %w", errs[0])
	}
	if len(rules) != 1 {
		return nil, fmt.Errorf("rule: not valid on this OS")
	}
	return text, nil
}

func renderPack(pack string, d ruleDoc) ([]byte, error) {
	var sb strings.Builder
	sb.WriteString("# Proposed by the scheduled agent and added by the user.\n")
	fmt.Fprintf(&sb, "pack = %s\n\n[[rule]]\nrev = 1\n", q(pack))
	for _, kv := range []struct{ k, v string }{{"id", d.ID}, {"category", d.Category}, {"bucket", d.Bucket}, {"kind", d.Kind}, {"min_age", d.MinAge}} {
		s, err := tomlString(kv.v)
		if err != nil {
			return nil, fmt.Errorf("rule: %s: %w", kv.k, err)
		}
		fmt.Fprintf(&sb, "%s = %s\n", kv.k, s)
	}
	for _, kv := range []struct {
		k string
		v []string
	}{{"names", d.Names}, {"markers", d.Markers}, {"os", d.OS}} {
		if len(kv.v) == 0 {
			continue
		}
		parts := make([]string, 0, len(kv.v))
		for _, e := range kv.v {
			s, err := tomlString(e)
			if err != nil {
				return nil, fmt.Errorf("rule: %s: %w", kv.k, err)
			}
			parts = append(parts, s)
		}
		fmt.Fprintf(&sb, "%s = [%s]\n", kv.k, strings.Join(parts, ", "))
	}
	return []byte(sb.String()), nil
}

func q(s string) string { return strconv.Quote(s) }

// tomlString quotes s as a TOML basic string; only printable ASCII is accepted, so no escape is ever needed
// beyond the quote and the backslash.
func tomlString(s string) (string, error) {
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return "", fmt.Errorf("only printable ASCII is allowed")
		}
	}
	return strconv.Quote(s), nil
}
