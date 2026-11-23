package strategies

import (
	"slices"
	"testing"

	"github.com/als0x/sapulidi/internal/core"
)

func vs(names ...string) []Version {
	var out []Version
	for _, n := range names {
		out = append(out, Version{Name: n, Path: "/x/" + n})
	}
	return out
}

func removed(d Decision) []string {
	var out []string
	for _, v := range d.Remove {
		out = append(out, v.Name)
	}
	slices.Sort(out)
	return out
}

func apply(t *testing.T, id string, in Input) Decision {
	t.Helper()
	d, ok := Apply(id, in)
	if !ok {
		t.Fatalf("strategy %s not found", id)
	}
	return d
}

func TestRegistered(t *testing.T) {
	for _, id := range []string{KeepActiveAndPrevious, KeepNewestPerName, NotPinnedInRoots} {
		if !core.StrategyKnown(id) {
			t.Errorf("%s is not registered with core", id)
		}
	}
	if _, ok := Apply("nope", Input{}); ok {
		t.Error("unknown strategy applied")
	}
}

func TestKeepActiveAndPrevious(t *testing.T) {
	in := Input{Versions: vs("v18.19.0", "v20.11.0", "v22.1.0", "v9.0.0"), Active: []string{"v20.11.0"}, ActiveKnown: true}
	d := apply(t, KeepActiveAndPrevious, in)
	// active v20.11.0 and the newest other v22.1.0 stay; v9 sorts below v18 numerically
	if got := removed(d); !slices.Equal(got, []string{"v18.19.0", "v9.0.0"}) {
		t.Fatalf("removed %v", got)
	}
	if d.Keep["v20.11.0"] != "active" || d.Keep["v22.1.0"] != "active" {
		t.Fatalf("keeps %v", d.Keep)
	}
	// the active version is not installed (system node): the newest installed one is still kept
	in.Active = []string{"system"}
	if got := removed(apply(t, KeepActiveAndPrevious, in)); !slices.Equal(got, []string{"v18.19.0", "v20.11.0", "v9.0.0"}) {
		t.Fatalf("removed %v", got)
	}
	// a pinned version is kept too
	in.Pinned = []string{"v9.0.0"}
	if got := removed(apply(t, KeepActiveAndPrevious, in)); !slices.Equal(got, []string{"v18.19.0", "v20.11.0"}) {
		t.Fatalf("removed with pin %v", got)
	}
}

func TestKeepNewestPerName(t *testing.T) {
	in := Input{Versions: vs("chromium-1140", "chromium-1148", "firefox-1450", "webkit-2000", "webkit-1900", "mystery")}
	d := apply(t, KeepNewestPerName, in)
	if got := removed(d); !slices.Equal(got, []string{"chromium-1140", "webkit-1900"}) {
		t.Fatalf("removed %v", got)
	}
	if _, ok := d.Keep["mystery"]; !ok {
		t.Fatal("a name without a build number must be kept")
	}
	// a build that is active stays even when it is not the newest
	in.Active = []string{"chromium-1140"}
	if got := removed(apply(t, KeepNewestPerName, in)); !slices.Equal(got, []string{"webkit-1900"}) {
		t.Fatalf("removed with active %v", got)
	}
}

func TestNotPinnedInRoots(t *testing.T) {
	in := Input{Versions: vs("stable", "nightly", "1.70.0"), Active: []string{"stable"}, ActiveKnown: true, Pinned: []string{"1.70.0"}}
	if got := removed(apply(t, NotPinnedInRoots, in)); !slices.Equal(got, []string{"nightly"}) {
		t.Fatalf("removed %v", got)
	}
}

func TestUnknownActiveKeepsAll(t *testing.T) {
	for _, id := range []string{KeepActiveAndPrevious, NotPinnedInRoots} {
		in := Input{Versions: vs("v1.0.0", "v2.0.0", "v3.0.0"), ActiveKnown: false}
		d := apply(t, id, in)
		if len(d.Remove) != 0 {
			t.Errorf("%s removed %v with an unknown active version", id, removed(d))
		}
		if len(d.Keep) != 3 {
			t.Errorf("%s keeps %v", id, d.Keep)
		}
	}
}

func TestCompare(t *testing.T) {
	if Compare("v20.1.0", "v9.0.0") <= 0 || Compare("1.10", "1.9") <= 0 || Compare("a", "a") != 0 {
		t.Fatal("numeric segments must compare as numbers")
	}
	for _, p := range [][2]string{
		{"v20.0.0-rc.1", "v20.0.0"}, {"go1.22rc1", "go1.22"}, {"go1.22beta1", "go1.22"}, {"1.0.0-alpha", "1.0.0"},
		{"go1.22rc1", "go1.22.1"}, {"v20.0.0-rc.1", "v20.0.1"}, {"v19.9.9", "v20.0.0-rc.1"},
	} {
		if Compare(p[0], p[1]) >= 0 || Compare(p[1], p[0]) <= 0 {
			t.Errorf("%s must sort below %s", p[0], p[1])
		}
	}
	if Compare("go1.22.1", "go1.22") <= 0 {
		t.Error("a point release sorts above its base")
	}
}
