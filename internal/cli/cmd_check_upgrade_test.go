package cli

import (
	"strings"
	"testing"
)

const upgradeText = "Sapu Lidi now asks before removing"

const optedIn = "[category.deps]\nenabled = true\n[category.worktrees]\nenabled = true\n"

func checkOutput(t *testing.T) string {
	t.Helper()
	code, out, e := runCLI(t, "check")
	if code != 0 {
		t.Fatalf("%d %q", code, e)
	}
	return out
}

func TestCheckSaysOnceThatEnabledCategoriesNeedConsent(t *testing.T) {
	consentFixture(t, optedIn, []string{safeKey})
	want := upgradeText + " deps, worktrees: run sapulidi consent\n"
	if out := checkOutput(t); !strings.Contains(out, want) {
		t.Fatalf("missing %q in %q", want, out)
	}
	if out := checkOutput(t); strings.Contains(out, upgradeText) {
		t.Fatalf("said twice: %q", out)
	}
}

func TestCheckUpgradeLineNamesOnlyUnconsentedCategories(t *testing.T) {
	consentFixture(t, optedIn, []string{safeKey, recKey})
	if out := checkOutput(t); !strings.Contains(out, upgradeText+" worktrees: ") {
		t.Fatalf("%q", out)
	}
}

func TestCheckNoUpgradeLine(t *testing.T) {
	for name, tc := range map[string]struct {
		config    string
		consented []string
	}{
		"nothing enabled in config": {"", []string{safeKey}},
		"switched off":              {"[category.deps]\nenabled = false\n", []string{safeKey}},
		"all consented":             {optedIn, []string{safeKey, recKey, callKey}},
		"auto category enabled":     {"[category.temp]\nenabled = true\n", []string{}},
		"never set up":              {optedIn, nil},
		"corrupt config":            {"= not toml", []string{safeKey}},
	} {
		t.Run(name, func(t *testing.T) {
			consentFixture(t, tc.config, tc.consented)
			if out := checkOutput(t); strings.Contains(out, upgradeText) {
				t.Fatalf("%q", out)
			}
		})
	}
}
