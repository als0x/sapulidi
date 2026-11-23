package core

import "testing"

type afterCat struct{ got []Candidate }

func (*afterCat) Name() string                           { return "x" }
func (*afterCat) Bucket() Bucket                         { return Optional }
func (*afterCat) Scan(*Env) ([]Candidate, []Kept, error) { return nil, nil, nil }
func (*afterCat) Proof(*Env, Candidate) error            { return nil }
func (a *afterCat) AfterRemove(_ *Env, c Candidate)      { a.got = append(a.got, c) }

func TestCategoryMayImplementAfterRemover(t *testing.T) {
	var c Category = &afterCat{}
	ar, ok := c.(AfterRemover)
	if !ok {
		t.Fatal("a category with AfterRemove must satisfy AfterRemover")
	}
	ar.AfterRemove(nil, Candidate{Path: "p"})
	if len(c.(*afterCat).got) != 1 {
		t.Fatal("AfterRemove not called")
	}
}
