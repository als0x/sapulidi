package testutil

import "github.com/als0x/sapulidi/internal/core"

type noExclusions struct{}

func (noExclusions) Match(string) (string, bool)  { return "", false }
func (noExclusions) Within(string) (string, bool) { return "", false }

// NoExclusions is an exclusion list that excludes nothing, for a test Env: the engine refuses to run when
// Env.Exclusions was never set.
var NoExclusions core.Excluder = noExclusions{}
