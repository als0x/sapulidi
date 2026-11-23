package versions

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/tools"
	"github.com/als0x/sapulidi/internal/tools/strategies"
)

// appleLocations are the location ids of apple.toml; the simulator rule is a cache rule and has none.
var appleLocations = map[string]bool{"xcode-derived-data": true, "xcode-device-support": true}

// keepDeviceSupport is how many iOS device support folders stay.
const keepDeviceSupport = 2

var simctlDeleteCmd = []string{"xcrun", "simctl", "delete", "unavailable"}

type appleCat struct{}

func (*appleCat) Name() string        { return "apple" }
func (*appleCat) Bucket() core.Bucket { return core.Optional }

func (a *appleCat) Scan(env *core.Env) ([]core.Candidate, []core.Kept, error) {
	var cands []core.Candidate
	var kept []core.Kept
	for i := range env.Rules {
		r := &env.Rules[i]
		if r.Category != "apple" || r.Bucket == core.Never {
			continue
		}
		cs, ks := a.plan(env, r)
		cands, kept = append(cands, cs...), append(kept, ks...)
	}
	return cands, kept, nil
}

func (a *appleCat) Proof(env *core.Env, c core.Candidate) error {
	return proof(env, "apple", c, a.plan)
}

func (*appleCat) plan(env *core.Env, rule *core.Rule) ([]core.Candidate, []core.Kept) {
	if env.Platform == nil || env.Platform.OS() != "darwin" {
		return nil, nil
	}
	switch {
	case rule.Kind == core.KindPath && rule.Location == "xcode-derived-data":
		return derivedData(env, rule)
	case rule.Kind == core.KindPath && rule.Location == "xcode-device-support":
		return deviceSupport(env, rule)
	case rule.Kind == core.KindCache && rule.Tool == "xcrun" && rule.Action == "simctl-delete-unavailable":
		return simulators(env, rule)
	}
	return nil, nil
}

func xcodeDir(env *core.Env, name string) string {
	return filepath.Join(env.Home, "Library", "Developer", "Xcode", name)
}

// derivedData proposes every project folder in DerivedData untouched for the category age.
func derivedData(env *core.Env, rule *core.Rule) (cands []core.Candidate, kept []core.Kept) {
	base := xcodeDir(env, "DerivedData")
	names, err := listDirs(base)
	if err != nil {
		return nil, nil
	}
	age := env.Settings.Age("apple", rule.MinAge)
	for _, n := range names {
		if strings.HasPrefix(n, ".") || strings.HasSuffix(n, ".noindex") {
			continue // ModuleCache.noindex and friends are shared, not a project's
		}
		p := filepath.Join(base, n)
		if youngerThan(env, p, age) {
			kept = append(kept, core.Kept{Category: "apple", RuleKey: rule.Key(), Path: p, Why: "young"})
			continue
		}
		cands = append(cands, core.Candidate{
			Category: "apple", RuleKey: rule.Key(), Path: p, Base: base, Removal: core.RenameDelete,
			Reason: "DerivedData of " + n + ", untouched for " + ageText(age),
		})
	}
	return cands, kept
}

var iosVersionRe = regexp.MustCompile(`\d+(?:\.\d+)+`)

// deviceSupport proposes every iOS DeviceSupport folder except the newest two. A folder whose version cannot
// be read is kept.
func deviceSupport(env *core.Env, rule *core.Rule) (cands []core.Candidate, kept []core.Kept) {
	base := xcodeDir(env, "iOS DeviceSupport")
	names, err := listDirs(base)
	if err != nil {
		return nil, nil
	}
	type dev struct{ name, ver string }
	groups := map[string][]dev{} // by the platform prefix before the version ("" or "iPhone15,2")
	var prefixes []string
	for _, n := range names {
		loc := iosVersionRe.FindStringIndex(n)
		if loc == nil || strings.HasPrefix(n, ".") {
			kept = append(kept, core.Kept{Category: "apple", RuleKey: rule.Key(), Path: filepath.Join(base, n), Why: "error"})
			continue
		}
		prefix := strings.TrimSpace(n[:loc[0]])
		if _, ok := groups[prefix]; !ok {
			prefixes = append(prefixes, prefix)
		}
		groups[prefix] = append(groups[prefix], dev{n, n[loc[0]:loc[1]]})
	}
	for _, prefix := range prefixes {
		devs := groups[prefix]
		slices.SortFunc(devs, func(a, b dev) int {
			if c := strategies.Compare(b.ver, a.ver); c != 0 {
				return c
			}
			return strings.Compare(b.name, a.name)
		})
		for i, d := range devs {
			p := filepath.Join(base, d.name)
			if i < keepDeviceSupport {
				kept = append(kept, core.Kept{Category: "apple", RuleKey: rule.Key(), Path: p, Why: "active"})
				continue
			}
			cands = append(cands, core.Candidate{
				Category: "apple", RuleKey: rule.Key(), Path: p, Base: base, Removal: core.RenameDelete,
				Reason: fmt.Sprintf("iOS device support %s: not one of the newest %d", d.name, keepDeviceSupport),
			})
		}
	}
	return cands, kept
}

// simulators proposes `xcrun simctl delete unavailable` only when simctl lists an unavailable device.
func simulators(env *core.Env, rule *core.Rule) ([]core.Candidate, []core.Kept) {
	if _, err := env.Run.LookPath("xcrun"); err != nil {
		return nil, nil
	}
	out, err := tools.Query(env, "xcrun", "simctl", "list", "devices", "unavailable")
	if err != nil || !strings.Contains(out, "(unavailable") {
		return nil, nil
	}
	base := filepath.Join(env.Home, "Library", "Developer", "CoreSimulator")
	dir := filepath.Join(base, "Devices")
	if _, err := os.Stat(dir); err != nil {
		return nil, nil
	}
	return []core.Candidate{{
		Category: "apple", RuleKey: rule.Key(), Path: dir, Base: base, Removal: core.ToolCommand, Command: simctlDeleteCmd,
		Reason: "simulators whose runtime is gone (xcrun simctl delete unavailable)",
	}}, nil
}
