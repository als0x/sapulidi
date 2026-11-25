package core

import (
	"strconv"
	"time"
)

type RuleKind string

const (
	KindTemp     RuleKind = "temp"     // entries directly in the temp dir
	KindProject  RuleKind = "project"  // directories inside roots, next to a marker
	KindFile     RuleKind = "file"     // files inside roots (crash dumps, logs)
	KindCache    RuleKind = "cache"    // a global cache cleaned by a known tool command
	KindVersions RuleKind = "versions" // versions directories filtered by a strategy
	KindPath     RuleKind = "path"     // a known per-user location (agent data), handled by its category
)

type Rule struct {
	Pack     string // pack file name without .toml
	ID       string // unique inside the pack
	Rev      int    // bumped when a rule is widened (spec S2)
	Category string
	Bucket   Bucket
	Kind     RuleKind
	Names    []string // glob on the base name of the entry
	Markers  []string // KindProject: globs of files that must exist next to the entry
	MinAge   time.Duration
	Tool     string   // KindCache: a tool id known to internal/tools
	Action   string   // KindCache: an action id known to internal/tools for that tool
	Strategy string   // KindVersions: a strategy id known to internal/tools
	Location string   // KindVersions, KindPath: a location id known to the category
	OS       []string // "darwin", "linux"; empty means both
	// Recommend says sapulidi stands behind removing this once the rule's guards and MinAge pass.
	// Only valid on Optional rules.
	Recommend bool
}

func (r Rule) Key() string { return r.Pack + "/" + r.ID + "@" + strconv.Itoa(r.Rev) }
