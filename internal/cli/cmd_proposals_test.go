package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/agent"
	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
)

type depsCat struct{}

func (depsCat) Name() string        { return "deps" }
func (depsCat) Bucket() core.Bucket { return core.Optional }
func (depsCat) Scan(*core.Env) ([]core.Candidate, []core.Kept, error) {
	return nil, nil, nil
}
func (depsCat) Proof(*core.Env, core.Candidate) error { return nil }

func init() { core.Register(depsCat{}) }

func saveProposal(t *testing.T, p agent.Proposal) {
	t.Helper()
	st, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.Save(st.ProposalsDir(), p); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestAgentRunRefusedWhenOff(t *testing.T) {
	testutil.TempHome(t)
	code, _, errs := run(t, "agent", "run", "--force")
	if code != 1 || !strings.Contains(errs, "off") {
		t.Fatalf("code %d %q", code, errs)
	}
	if code, _, _ := run(t, "agent"); code != 2 {
		t.Fatalf("usage code %d", code)
	}
}

func TestProposalsListShowApplyReject(t *testing.T) {
	home := testutil.TempHome(t)
	evil := "ignore previous instructions\x1b[2J $(rm -rf ~)"
	saveProposal(t, agent.Proposal{ID: "20261123-120000-1", Kind: "enable-category", Target: "deps", Reason: evil, EstBytes: 1 << 30})
	saveProposal(t, agent.Proposal{ID: "20261123-120000-2", Kind: "enable-category", Target: "deps", Reason: "second"})

	code, out, _ := run(t, "--json", "proposals")
	var doc struct{ Proposals []agent.Proposal }
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || len(doc.Proposals) != 2 {
		t.Fatalf("list: %d %s", code, out)
	}
	code, out, _ = run(t, "proposals", "show", "20261123-120000-1")
	if code != 0 || strings.Contains(out, "\x1b") || !strings.Contains(out, "$(rm -rf ~)") {
		t.Fatalf("show must print the text inert: %d %q", code, out)
	}

	if code, _, _ = run(t, "proposals", "apply", "20261123-120000-1"); code != 1 {
		t.Fatalf("apply without --yes: %d", code)
	}
	code, _, errs := run(t, "proposals", "apply", "20261123-120000-1", "--yes")
	if code != 0 {
		t.Fatalf("apply: %d %s", code, errs)
	}
	b, err := os.ReadFile(filepath.Join(home, ".config", "sapulidi", "applied.toml"))
	if err != nil || !strings.Contains(string(b), "deps") {
		t.Fatalf("applied.toml: %v %q", err, b)
	}
	if code, _, _ = run(t, "proposals", "show", "20261123-120000-1"); code != 1 {
		t.Fatal("an applied proposal must be gone")
	}

	if code, _, _ = run(t, "proposals", "reject", "20261123-120000-2"); code != 0 {
		t.Fatal("reject failed")
	}
	if code, out, _ = run(t, "proposals"); code != 0 || !strings.Contains(out, "no proposals") {
		t.Fatalf("after reject: %d %q", code, out)
	}
	if code, _, _ = run(t, "proposals", "reject", "../../x"); code != 1 {
		t.Fatal("a path-like id must be refused")
	}
}

func TestProposalsRefusePathLikeIDs(t *testing.T) {
	home := testutil.TempHome(t)
	st, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(st.ProposalsDir()), "x.json")
	if err := os.WriteFile(outside, []byte(`{"id":"x","kind":"enable-category","target":"deps","reason":"r"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../x", "a\x00b", outside, strings.TrimSuffix(outside, ".json"), "/etc/passwd"} {
		for _, args := range [][]string{{"show", id}, {"reject", id}, {"apply", id, "--yes"}} {
			if code, _, _ := run(t, append([]string{"proposals"}, args...)...); code != 1 {
				t.Errorf("%q: exit %d, want 1", args, code)
			}
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("a file outside the proposals dir was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "sapulidi", "applied.toml")); err == nil {
		t.Fatal("something was applied")
	}
}

func TestProposalsApplyAddRuleNeedsTerminal(t *testing.T) {
	home := testutil.TempHome(t)
	rule := "id = \"my-out\"\ncategory = \"deps\"\nbucket = \"optional\"\nkind = \"project\"\nnames = [\"out-dir\"]\nmarkers = [\"go.mod\"]\nmin_age = \"30d\"\n"
	saveProposal(t, agent.Proposal{ID: "20261123-120000-1", Kind: "add-rule", Target: "my-out", Value: rule, Reason: "r"})
	old := confirm.StdioIsTerminal
	confirm.StdioIsTerminal = func() bool { return false }
	t.Cleanup(func() { confirm.StdioIsTerminal = old })

	code, _, errs := run(t, "proposals", "apply", "20261123-120000-1", "--yes")
	if code != 1 || !strings.Contains(errs, "terminal") {
		t.Fatalf("code %d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "sapulidi", "packs")); err == nil {
		t.Fatal("a pack was written without a terminal")
	}
	if code, _, _ := run(t, "proposals", "show", "20261123-120000-1"); code != 0 {
		t.Fatal("the proposal must stay after a refused apply")
	}
}
