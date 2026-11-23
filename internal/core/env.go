package core

import (
	"context"
	"time"
)

type Platform interface {
	OS() string // "darwin" | "linux"
	IsWSL() bool
	TempDir() (string, error)              // spec 4.2
	ScratchBases() []string                // existing, user-owned agent scratch bases (spec 4.2)
	IsOpen(path string) (bool, error)      // any open file or process cwd under path; error means "in use"
	ArgsMention(path string) (bool, error) // a live process has path in its arguments
	SameDevice(a, b string) (bool, error)
	OwnedByUser(path string) (bool, error)
	FreeBytes(path string) (uint64, error) // bytes available to the user (statfs)
	Notify(title, body string) error
	HasTTY() bool // /dev/tty can be opened for reading and writing
}

type Runner interface {
	// Run runs argv in dir and returns stdout. A non-zero exit is an error that wraps the exit code
	// and stderr. ctx carries the timeout.
	Run(ctx context.Context, dir string, argv ...string) ([]byte, error)
	LookPath(name string) (string, error)
}

type Settings interface {
	CategoryEnabled(category string) bool // auto categories: true; optional: config
	RuleEnabled(ruleKey string) bool      // config may disable one rule
	Age(category string, def time.Duration) time.Duration
	Consented(ruleKey string) bool // spec S2; false for every rule before setup
	LowDiskBytes() uint64          // config low_disk_gb
	GrowthBytesPerWeek() int64     // config growth_gb_per_week
	NotifyEnabled() bool           // config notify.enabled
}

type Logger interface {
	Event(kind string, fields map[string]any)
}

// FactSink collects facts a category wants to tell the user once (spec 6 notifications). The engine
// passes them to state.Store.Fact at the end of a run and notifies the changed ones.
type FactSink interface {
	Fact(key, text string)
}

type Env struct {
	Home     string
	Now      time.Time
	Roots    []Root
	Rules    []Rule
	Platform Platform
	Run      Runner
	Settings Settings
	Log      Logger
	Facts    FactSink
	Ctx      context.Context // run budget; categories stop scanning when it is done
	TempDir  string          // Platform.TempDir() resolved once per run; "" when unknown
}

// Category turns rules into candidates. Scan must not modify anything.
type Category interface {
	Name() string
	Bucket() Bucket
	Scan(env *Env) ([]Candidate, []Kept, error)
	// Proof re-checks the category-specific proof right before removal (spec S8).
	// nil means still removable; a *KeepError means keep.
	Proof(env *Env, c Candidate) error
}

var categories []Category

func Register(c Category)    { categories = append(categories, c) }
func Categories() []Category { return append([]Category(nil), categories...) }
