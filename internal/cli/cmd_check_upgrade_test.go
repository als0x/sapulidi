package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/testutil"
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
	want := upgradeText + " deps, worktrees: run /sapulidi consent " +
		"(or /sapulidi consent --yes --recommended for the recommended rules)\n"
	if out := checkOutput(t); !strings.Contains(out, want) {
		t.Fatalf("missing %q in %q", want, out)
	}
	if out := checkOutput(t); strings.Contains(out, upgradeText) {
		t.Fatalf("said twice: %q", out)
	}
}

func TestCheckUpgradeLineNamesOnlyUnconsentedCategories(t *testing.T) {
	consentFixture(t, optedIn, []string{safeKey, recKey})
	// no recommended rule waits, so the flag is not mentioned
	if out := checkOutput(t); !strings.Contains(out, upgradeText+" worktrees: run /sapulidi consent\n") ||
		strings.Contains(out, "--recommended") {
		t.Fatalf("%q", out)
	}
}

// markerProbe notes whether the marker existed when the upgrade line reached the output.
type markerProbe struct {
	marker     string
	said, held bool
}

func (p *markerProbe) Write(b []byte) (int, error) {
	if strings.Contains(string(b), upgradeText) {
		_, err := os.Lstat(p.marker)
		p.said, p.held = true, err == nil
	}
	return len(b), nil
}

func TestCheckWritesTheMarkerBeforeTheLine(t *testing.T) {
	consentFixture(t, optedIn, []string{safeKey})
	paths, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	p := &markerProbe{marker: filepath.Join(paths.State, upgradeMarker)}
	if code := Main([]string{"check"}, p, io.Discard); code != 0 || !p.said || !p.held {
		t.Fatalf("code %d, said %v, marker first %v", code, p.said, p.held)
	}
}

func TestUpgradeLineLogsAMarkerThatCannotBeWritten(t *testing.T) {
	on := true
	cfg := &config.Config{Categories: map[string]config.CategoryConf{"deps": {Enabled: &on}}}
	rules := []core.Rule{{Pack: "x", ID: "rec", Rev: 1, Category: "deps", Bucket: core.Optional, Recommend: true}}
	log := &testutil.MemLogger{}
	line := upgradeLine(filepath.Join(t.TempDir(), "missing"), rules, &config.Consent{}, cfg, log)
	if !strings.Contains(line, upgradeText+" deps: ") {
		t.Fatalf("line %q", line)
	}
	if len(log.Events) != 1 || log.Events[0].Kind != "upgrade-marker-not-written" || log.Events[0].Fields["error"] == "" {
		t.Fatalf("log %+v", log.Events)
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
