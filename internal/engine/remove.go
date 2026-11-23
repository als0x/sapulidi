package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/guard"
)

const (
	removingPrefix = ".sapulidi-removing-"
	toolTimeout    = 10 * time.Minute
)

// guards runs the guards of the plan's table "Removal kinds and the guards the engine runs", in order.
// The category's Proof is last. Any non-nil result keeps the candidate.
func (r *runner) guards(cat core.Category, c core.Candidate, rule core.Rule) error {
	e := r.env
	switch c.Removal {
	case core.RenameDelete:
		if err := r.placed(c); err != nil {
			return err
		}
		if err := guard.Secret(c.Path); err != nil {
			return err
		}
		if err := guard.Fresh(c.Path, rule.MinAge, e.Now); err != nil {
			return err
		}
		if err := guard.InUse(e.Platform, c.Path, c.Category == "temp"); err != nil {
			return err
		}
	case core.WorktreeRemove:
		if c.Dir == "" {
			return core.Keep("error", "worktree without main repository dir")
		}
		if _, err := e.Run.LookPath("git"); err != nil {
			return core.Keep("error", "git not found")
		}
		if err := r.placed(c); err != nil {
			return err
		}
		if err := guard.Secret(c.Path); err != nil {
			return err
		}
		if err := guard.InUse(e.Platform, c.Path, false); err != nil {
			return err
		}
	case core.ToolCommand:
		if c.Path != "" {
			if err := r.placed(c); err != nil {
				return err
			}
		}
		if len(c.Command) == 0 || !core.ToolKnown(c.Command[0]) {
			return core.Keep("error", "unknown tool")
		}
		if _, err := e.Run.LookPath(c.Command[0]); err != nil {
			return core.Keep("error", "tool not found: "+c.Command[0])
		}
	default:
		return core.Keep("error", "unknown removal kind "+string(c.Removal))
	}
	return safeProof(cat, e, c)
}

// placed is the part of the guards that must hold again right before a rename: the base is one the engine
// allows, the path is not denied, and it sits below the base with no symlink in between (spec S3, S4).
func (r *runner) placed(c core.Candidate) error {
	e := r.env
	// A tool's cache dir may sit directly in home (~/.npm): Base == Home is accepted only for a ToolCommand
	// that names that dir; Deny and Place below still apply to the dir itself.
	homeCache := c.Removal == core.ToolCommand && c.Path != "" && e.Home != "" && c.Base == e.Home
	if !homeCache {
		if err := baseAllowed(e, c.Base); err != nil {
			return err
		}
	}
	if err := guard.Deny(e.Home, e.TempDir, c.Path); err != nil {
		return err
	}
	return guard.Place(e.Platform, c.Base, c.Path)
}

func below(base, p string) bool { return p == base || strings.HasPrefix(p, base+"/") }

// baseAllowed keeps a candidate whose Base is not a place sapulidi works in: the roots, the temp dir, the agent
// scratch bases, /tmp, or a hidden folder or Library inside home. Home itself and ordinary home folders such as
// Documents are never a base.
//
// Note for category authors: a Base in a non-dot home folder (for example ~/Code) is allowed only when it is
// (under) a registered Root in env.Roots; otherwise the candidate is kept with "place".
func baseAllowed(e *core.Env, base string) error {
	if !filepath.IsAbs(base) || filepath.Clean(base) != base || base == "/" {
		return core.Keep("place", "base is not an absolute, clean, non-root path: "+base)
	}
	// A home that itself lives under /tmp (HOME=/tmp/x, CI) must not turn every home folder into an allowed base:
	// below home, only the home rules further down and the explicit entries (temp dir, roots, scratch) apply.
	inHome := e.Home != "" && below(e.Home, base)
	var allowed []string
	if !inHome {
		allowed = append(allowed, "/tmp", "/private/tmp")
	}
	if e.TempDir != "" {
		allowed = append(allowed, e.TempDir)
	}
	for _, root := range e.Roots {
		allowed = append(allowed, root.Path)
	}
	if e.Platform != nil {
		allowed = append(allowed, e.Platform.ScratchBases()...)
	}
	for _, a := range allowed {
		if a != "" && a != "/" && below(a, base) {
			return nil
		}
	}
	if e.Home != "" && strings.HasPrefix(base, e.Home+"/") {
		first, _, _ := strings.Cut(base[len(e.Home)+1:], "/")
		if strings.HasPrefix(first, ".") || first == "Library" {
			return nil
		}
	}
	return core.Keep("place", "base is not an allowed location: "+base)
}

// remove removes one candidate whose guards passed. A nil error means the removal is committed.
func (r *runner) remove(cat core.Category, c core.Candidate, rule core.Rule) error {
	switch c.Removal {
	case core.RenameDelete:
		return r.renameDelete(cat, c, rule)
	case core.WorktreeRemove:
		_, err := r.runTool(c.Dir, guard.GitArgv(c.Dir, "worktree", "remove", "--", c.Path)...)
		return err
	case core.ToolCommand:
		dir := c.Dir
		if dir == "" {
			dir = r.env.Home
		}
		_, err := r.runTool(dir, c.Command...)
		return err
	}
	return errors.New("unknown removal kind")
}

func (r *runner) runTool(dir string, argv ...string) ([]byte, error) {
	// Its own timeout, not the run budget: a removal that started finishes (spec S8).
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()
	out, err := r.env.Run.Run(ctx, dir, argv...)
	code := 0
	if err != nil {
		code = -1
		var ee *execx.ExitError
		if errors.As(err, &ee) {
			code = ee.Code
		}
	}
	r.event("tool", map[string]any{"argv": argv, "exit": code})
	return out, err
}

// parentIsReal checks that the parent directory is not a symlink and resolves to base/<its relative path>.
func parentIsReal(base, parent string) error {
	pi, err := os.Lstat(parent)
	if err != nil {
		return core.Keep("place", "parent: "+err.Error())
	}
	if pi.Mode()&os.ModeSymlink != 0 || !pi.IsDir() {
		return core.Keep("place", "parent is a symlink or not a directory: "+parent)
	}
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return core.Keep("place", "base: "+err.Error())
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return core.Keep("place", "parent: "+err.Error())
	}
	rel, err := filepath.Rel(base, parent)
	if err != nil || realParent != filepath.Join(realBase, rel) {
		return core.Keep("place", "symlink in parent: "+parent)
	}
	return nil
}

func (r *runner) renameDelete(cat core.Category, c core.Candidate, rule core.Rule) error {
	// Re-check right before the commit point: a proof, sizing or a slow scan may have run since the first
	// check. A *KeepError here keeps the candidate.
	e := r.env
	if err := r.placed(c); err != nil {
		return err
	}
	if err := guard.Secret(c.Path); err != nil {
		return err
	}
	if err := guard.Fresh(c.Path, rule.MinAge, e.Now); err != nil {
		return err
	}
	if err := guard.InUse(e.Platform, c.Path, c.Category == "temp"); err != nil {
		return err
	}
	// The sizing walk before this point can take seconds on a large tree: what the category's Proof saw (ignored,
	// tracked, marker, no repository inside) must still hold.
	if err := safeProof(cat, e, c); err != nil {
		return err
	}
	parent := filepath.Dir(c.Path)
	if err := parentIsReal(c.Base, parent); err != nil {
		return err
	}
	var renamed string
	for attempt := 0; ; attempt++ {
		if attempt == 5 {
			return errors.New("no free name for the removing entry")
		}
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		renamed = filepath.Join(parent, removingPrefix+hex.EncodeToString(b[:]))
		if _, err := os.Lstat(renamed); errors.Is(err, fs.ErrNotExist) {
			break
		}
	}
	if err := os.Rename(c.Path, renamed); err != nil { // the commit point
		return err
	}
	r.event("remove-start", map[string]any{"path": c.Path, "renamed": renamed, "parent": parent, "base": c.Base,
		"category": c.Category, "rule": c.RuleKey})
	if err := os.RemoveAll(renamed); err != nil {
		// Committed: the next run's Finish completes it.
		r.event("remove-error", map[string]any{"renamed": renamed, "error": err.Error()})
		return nil
	}
	r.event("remove", map[string]any{"path": c.Path, "renamed": renamed, "bytes": c.Bytes})
	return nil
}
