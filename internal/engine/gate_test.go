package engine

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
)

// gateKinds are the three kinds of removable rule: the category it lives in and the label it must get.
var gateKinds = []struct {
	name, category string
	bucket         core.Bucket
	recommend      bool
	label          core.Label
}{
	{"auto", "temp", core.Auto, false, core.LabelSafe},
	{"optional-recommended", "deps", core.Optional, true, core.LabelRecommended},
	{"optional-other", "deps", core.Optional, false, core.LabelYourCall},
}

func TestConsentGateMatrix(t *testing.T) {
	for _, kind := range gateKinds {
		for _, consented := range []bool{true, false} {
			for _, mode := range []string{"dry", "run", "explicit", "include-recommended", "include-recommended-dry"} {
				name := kind.name + "/" + map[bool]string{true: "consented", false: "unconsented"}[consented] + "/" + mode
				t.Run(name, func(t *testing.T) {
					f := newFix(t)
					key := "x/gate@1"
					f.env.Rules = []core.Rule{{Pack: "x", ID: "gate", Rev: 1, Category: kind.category, Bucket: kind.bucket,
						Recommend: kind.recommend, Kind: core.KindTemp, MinAge: f.env.Rules[0].MinAge}}
					f.set.ConsentAll = false
					f.set.ConsentedKeys = map[string]bool{key: consented}
					p := f.mk("a")
					c := f.cand(p)
					c.Category, c.RuleKey = kind.category, key
					var opt Options
					switch mode {
					case "dry":
						opt.DryRun = true
					case "explicit":
						opt.Explicit = []string{p}
					case "include-recommended":
						opt.IncludeRecommended = true
					case "include-recommended-dry":
						opt.IncludeRecommended, opt.DryRun = true, true
					}
					s := f.do(opt, f.cat(kind.category, kind.bucket, c))

					var remove bool
					switch mode {
					case "run":
						remove = consented
					case "explicit":
						remove = true
					case "include-recommended":
						remove = consented || kind.recommend
					}
					if exists(p) == remove || hasPath(s.Removed, p) != remove || hasPath(s.WouldRemove, p) == remove {
						t.Fatalf("remove = %v wanted: exists=%v %+v", remove, exists(p), s)
					}
					waiting := !remove && !consented && mode != "include-recommended-dry"
					if mode == "include-recommended-dry" {
						waiting = !consented && !kind.recommend
					}
					// the waiting notice counts only what setup or consent --yes can confirm
					waiting = waiting && kind.label != core.LabelYourCall
					if slices.Contains(s.Waiting, key) != waiting {
						t.Fatalf("waiting = %v, want %v", s.Waiting, waiting)
					}
					for _, got := range append(s.Removed, s.WouldRemove...) {
						if got.Label != kind.label {
							t.Fatalf("label %q, want %q", got.Label, kind.label)
						}
					}
				})
			}
		}
	}
}

// Before setup there is no consent record: IncludeRecommended removes the recommended rows and nothing else, even if
// the settings claim consent.
func TestIncludeRecommendedWithoutConsentRecord(t *testing.T) {
	f := newFix(t)
	if err := os.Remove(filepath.Join(f.home, ".local", "state", "sapulidi", "consent.json")); err != nil {
		t.Fatal(err)
	}
	f.env.Rules[1].Recommend = true
	a, d := f.mk("a"), f.mk("d")
	dc := f.cand(d)
	dc.Category, dc.RuleKey = "deps", "x/deps@1"
	s := f.do(Options{IncludeRecommended: true}, f.cat("temp", core.Auto, f.cand(a)), f.cat("deps", core.Optional, dc))
	if !exists(a) || exists(d) || s.Mode != "run" || !hasPath(s.WouldRemove, a) || !hasPath(s.Removed, d) {
		t.Fatalf("%+v", s)
	}
}

func TestLabelsInPlanAndQuickRun(t *testing.T) {
	f := newFix(t)
	f.env.Rules[1].Recommend = true
	a, d, w := f.mk("a"), f.mk("d"), f.mk("w")
	dc, wc := f.cand(d), f.cand(w)
	dc.Category, dc.RuleKey = "deps", "x/deps@1"
	wc.Category, wc.RuleKey = "worktrees", "x/wt@1"
	cats := []core.Category{f.cat("temp", core.Auto, f.cand(a)), f.cat("deps", core.Optional, dc),
		f.cat("worktrees", core.Optional, wc)}
	want := map[string]core.Label{a: core.LabelSafe, d: core.LabelRecommended, w: core.LabelYourCall}
	for name, s := range map[string]Summary{
		"plan":  Plan(f.env, cats, Options{}),
		"quick": f.do(Options{Quick: true, DryRun: true}, cats...),
	} {
		// a quick run leaves out the slow categories
		if len(s.WouldRemove) < 2 || (name == "plan" && len(s.WouldRemove) != len(want)) {
			t.Fatalf("%s: %+v", name, s)
		}
		for _, c := range s.WouldRemove {
			if c.Label != want[c.Path] {
				t.Errorf("%s %s: label %q, want %q", name, c.Path, c.Label, want[c.Path])
			}
		}
	}
}

func TestWaitingNoticeNamesTheCommand(t *testing.T) {
	const setup, rec = "sapulidi setup", "sapulidi consent --yes --recommended"
	for name, tc := range map[string]struct {
		safe, recommended bool
		want              string
	}{
		"safe":        {safe: true, want: "1 rule(s) wait for your confirmation: run " + setup + "."},
		"recommended": {recommended: true, want: "1 rule(s) wait for your confirmation: run " + rec + "."},
		"both": {safe: true, recommended: true,
			want: "2 rule(s) wait for your confirmation: run " + setup + " and " + rec + "."},
		"your call only": {},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFix(t)
			f.set.Notify, f.set.ConsentAll = true, false
			var cats []core.Category
			if tc.safe {
				cats = append(cats, f.cat("temp", core.Auto, f.cand(f.mk("a"))))
			}
			// deps is recommended in the "recommended" cases; worktrees is always a your-call rule here
			f.env.Rules[1].Recommend = true
			if tc.recommended {
				c := f.cand(f.mk("d"))
				c.Category, c.RuleKey = "deps", "x/deps@1"
				cats = append(cats, f.cat("deps", core.Optional, c))
			}
			w := f.cand(f.mk("w"))
			w.Category, w.RuleKey = "worktrees", "x/wt@1"
			cats = append(cats, f.cat("worktrees", core.Optional, w))
			f.do(Options{}, cats...)
			var want []string
			if tc.want != "" {
				want = []string{"sapulidi: " + tc.want}
			}
			if !slices.Equal(f.plat.Notifications, want) {
				t.Fatalf("notifications %q, want %q", f.plat.Notifications, want)
			}
		})
	}
}

func TestLabelWords(t *testing.T) {
	for l, want := range map[core.Label]string{
		core.LabelSafe: "safe", core.LabelRecommended: "recommended",
		core.LabelYourCall: "your call", core.LabelProtected: "protected",
	} {
		if got := l.Words(); got != want {
			t.Errorf("%q: %q, want %q", string(l), got, want)
		}
	}
}

// A 0.2.0 user who enabled deps in config.toml consented only auto rules: the run removes nothing of deps.
func TestUpgradeEnabledOptionalNeedsConsent(t *testing.T) {
	f := newFix(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[category.deps]\nenabled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f.env.Rules[1].Recommend = true
	f.env.Settings = config.NewSettings(cfg, &config.Consent{Rules: []string{"x/a@1"}},
		map[string]core.Bucket{"temp": core.Auto, "deps": core.Optional})
	a, d := f.mk("a"), f.mk("d")
	dc := f.cand(d)
	dc.Category, dc.RuleKey = "deps", "x/deps@1"
	s := f.do(Options{}, &fakeCat{name: "temp", bucket: core.Auto, cands: []core.Candidate{f.cand(a)}},
		&fakeCat{name: "deps", bucket: core.Optional, cands: []core.Candidate{dc}})
	if exists(a) || !exists(d) || !hasPath(s.WouldRemove, d) || !slices.Equal(s.Waiting, []string{"x/deps@1"}) {
		t.Fatalf("%+v", s)
	}
}
