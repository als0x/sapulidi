package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
)

// agent-scratch is auto and its proof is names, age and transcript inactivity: a clone made in an agent's scratch
// directory is somebody's work and must stay (nested-git), in Scan and again in Proof.
func TestScratchKeepsSessionHoldingRepo(t *testing.T) {
	home := testutil.TempHome(t)
	base := mkdir(t, filepath.Join(home, "claude-base"), time.Hour)
	repo := mkdir(t, filepath.Join(base, "-work-proj", id1), 3*day)
	write(t, filepath.Join(repo, "repo", ".git", "HEAD"), "ref: refs/heads/main\n", 3*day)
	mkdir(t, filepath.Join(repo, "repo", ".git"), 3*day)
	mkdir(t, filepath.Join(repo, "repo"), 3*day)
	mkdir(t, repo, 3*day)
	plain := mkdir(t, filepath.Join(base, "-work-proj", id2), 3*day)
	write(t, filepath.Join(plain, "repo", "main.go"), "package main\n", 3*day)
	mkdir(t, filepath.Join(plain, "repo"), 3*day)
	mkdir(t, plain, 3*day)
	e := scratchEnv(home, base)
	cands, kept := scan(t, "agent-scratch", e)
	wantPaths(t, cands, plain)
	if len(kept) != 1 || kept[0].Path != repo || kept[0].Why != "nested-git" {
		t.Fatalf("kept = %v", kept)
	}
	// Proof alone refuses it as well, in case a repository appears after the scan.
	c := core.Candidate{Category: "agent-scratch", RuleKey: "agent/claude-scratch@1", Path: repo,
		Base: filepath.Join(base, "-work-proj"), Removal: core.RenameDelete}
	err := category(t, "agent-scratch").Proof(e, c)
	if ke, ok := err.(*core.KeepError); !ok || ke.Why != "nested-git" {
		t.Fatalf("proof = %v, want keep nested-git", err)
	}
	if err := category(t, "agent-scratch").Proof(e, cands[0]); err != nil {
		t.Fatalf("proof of the plain session: %v", err)
	}
}

func TestScratchCodexKeepsEntryHoldingRepo(t *testing.T) {
	home := testutil.TempHome(t)
	base := mkdir(t, filepath.Join(home, ".codex", "tmp"), time.Hour)
	clone := filepath.Join(base, "clone")
	write(t, filepath.Join(clone, ".git", "HEAD"), "ref: refs/heads/main\n", 3*day)
	mkdir(t, filepath.Join(clone, ".git"), 3*day)
	mkdir(t, clone, 3*day)
	plain := mkdir(t, filepath.Join(base, "plain"), 3*day)
	e := scratchEnv(home, base)
	cands, kept := scan(t, "agent-scratch", e)
	wantPaths(t, cands, plain)
	if len(kept) != 1 || kept[0].Path != clone || kept[0].Why != "nested-git" {
		t.Fatalf("kept = %v", kept)
	}
	c := core.Candidate{Category: "agent-scratch", RuleKey: "agent/codex-tmp@1", Path: clone, Base: base, Removal: core.RenameDelete}
	err := category(t, "agent-scratch").Proof(e, c)
	if ke, ok := err.(*core.KeepError); !ok || ke.Why != "nested-git" {
		t.Fatalf("proof = %v, want keep nested-git", err)
	}
}

// A cancelled run keeps what it could not walk (a walk error keeps).
func TestScratchCancelledWalkKeeps(t *testing.T) {
	home := testutil.TempHome(t)
	base := mkdir(t, filepath.Join(home, "claude-base"), time.Hour)
	sess := mkdir(t, filepath.Join(base, "-work-proj", id1), 3*day)
	e := scratchEnv(home, base)
	cands, _ := scan(t, "agent-scratch", e)
	wantPaths(t, cands, sess)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.Ctx = ctx
	err := category(t, "agent-scratch").Proof(e, cands[0])
	if _, ok := err.(*core.KeepError); !ok {
		t.Fatalf("proof with a cancelled context = %v, want a keep", err)
	}
}
