package core

import "time"

type Bucket string

const (
	Auto     Bucket = "auto"
	Optional Bucket = "optional"
	Never    Bucket = "never"
)

type Removal string

const (
	RenameDelete   Removal = "rename-delete"   // engine renames to <parent>/.sapulidi-removing-<id>, then deletes
	ToolCommand    Removal = "tool-command"    // engine runs Candidate.Command
	WorktreeRemove Removal = "worktree-remove" // engine runs git worktree remove (no --force)
	ReportOnly     Removal = "report-only"     // never removed; shown by report
)

// Candidate is one thing a category proposes to remove.
type Candidate struct {
	Category string // category name from the table "Categories" in the plan
	RuleKey  string // Rule.Key(), e.g. "go/go-build@1"
	Path     string // absolute path; for ToolCommand the directory the tool reports
	Base     string // the allowed base Path must resolve under (spec S4)
	Removal  Removal
	Command  []string  // ToolCommand only: argv, argv[0] a known tool id
	Dir      string    // WorktreeRemove only: main repository dir to run git in
	Reason   string    // plain words, shown by plan
	Bytes    int64     // set by the engine; -1 when unknown
	Created  time.Time // birth time of Path, set by the engine; zero = unknown
	Updated  time.Time // newest change under Path, set by the engine; zero = unknown
	Source   string    // "auto" | "optional-on" (set by the engine)
}

// Kept is something a category or a guard decided to keep. Why is one token:
// young, open, process, secret, tracked, not-ignored, no-marker, nested-git, place, deny,
// dirty, untracked, stash, unpushed, gitfail, orphan, active, error, budget.
type Kept struct {
	Category, RuleKey, Path, Why string
}

// KeepError is returned by guards and Proof; Why is a Kept token.
type KeepError struct{ Why, Detail string }

func (e *KeepError) Error() string { return e.Why + ": " + e.Detail }

func Keep(why, detail string) error { return &KeepError{Why: why, Detail: detail} }

type Root struct {
	Path   string // absolute, top-level of a git repository or a configured directory
	Source string // "config" | "claude" | "codex" | "claude+children" | "codex+children"
}
