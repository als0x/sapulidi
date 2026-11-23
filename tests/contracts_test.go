package tests

import (
	"testing"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
	"github.com/als0x/sapulidi/internal/testutil"
)

// Compile-time checks: the fakes and the real runner satisfy the shared contracts.
var (
	_ core.Platform = (*testutil.FakePlatform)(nil)
	_ core.Runner   = execx.Real{}
	_ core.Runner   = (*testutil.FakeRunner)(nil)
	_ core.Settings = (*testutil.FakeSettings)(nil)
	_ core.Logger   = (*testutil.MemLogger)(nil)
	_ core.FactSink = (*testutil.MemFacts)(nil)
)

func TestContractsCompile(t *testing.T) {}
