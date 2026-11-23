package core

import "testing"

func TestRegistryRoundTrip(t *testing.T) {
	if ToolKnown("zz") || ToolActionKnown("zz", "a") || StrategyKnown("s-rt") || LocationKnown("l-rt") {
		t.Fatal("unexpected known names")
	}
	RegisterToolAction("zz", "a")
	RegisterStrategy("s-rt")
	RegisterLocation("l-rt")
	if !ToolKnown("zz") || !ToolActionKnown("zz", "a") || ToolActionKnown("zz", "b") {
		t.Fatal("tool actions wrong")
	}
	if !StrategyKnown("s-rt") || !LocationKnown("l-rt") {
		t.Fatal("strategy or location missing")
	}
}

type fakeCat struct {
	name string
	b    Bucket
}

func (f fakeCat) Name() string                           { return f.name }
func (f fakeCat) Bucket() Bucket                         { return f.b }
func (f fakeCat) Scan(*Env) ([]Candidate, []Kept, error) { return nil, nil, nil }
func (f fakeCat) Proof(*Env, Candidate) error            { return nil }

func TestCategoryBucketFromRegistered(t *testing.T) {
	before := len(Categories())
	Register(fakeCat{"cb-test", Optional})
	if len(Categories()) != before+1 {
		t.Fatal("not registered")
	}
	if b, ok := CategoryBucket("cb-test"); !ok || b != Optional {
		t.Fatalf("got %q %v", b, ok)
	}
	if _, ok := CategoryBucket("nope"); ok {
		t.Fatal("unknown category reported")
	}
}
