package agent

import "testing"

// The pause file stops every run (spec S11): the agent run checks it before it spends anything.
func TestAgentRunPaused(t *testing.T) {
	f := newFix(t)
	f.answer()
	f.st.Pause()
	res, err := Run(f.deps(), RunOpts{Force: true}) // --force skips the timing, not the pause
	if err != nil {
		t.Fatal(err)
	}
	if res.Ran || res.Reason != "paused" {
		t.Fatalf("result %+v, want not ran with reason paused", res)
	}
	if len(f.run.Calls) != 0 {
		t.Fatalf("a paused agent ran: %v", f.run.Calls)
	}
	f.st.Resume()
	if res, err := Run(f.deps(), RunOpts{Force: true}); err != nil || !res.Ran {
		t.Fatalf("after resume: %+v %v", res, err)
	}
}
