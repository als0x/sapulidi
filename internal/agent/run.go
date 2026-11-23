package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/internal/state/atomic"
)

// Timeout is the longest one agent run may take.
const Timeout = 10 * time.Minute

const (
	defaultInterval = 7 * 24 * time.Hour
	anomalyInterval = 24 * time.Hour
)

// ErrDisabled means [agent] enabled is not true. --force does not override it.
var ErrDisabled = errors.New("the scheduled agent is off: set [agent] enabled = true to opt in")

// RunDeps are the things a run needs; tests replace the runner and the clock.
type RunDeps struct {
	Cfg     *config.Config
	Paths   config.Paths
	Store   *state.Store
	Runner  core.Runner // use NewRunner for the real agent: a minimal environment and a limit on the output
	Bin     string      // absolute, symlink-free path of the running sapulidi binary (the agent may run only it)
	Now     time.Time
	Anomaly func() bool        // true when a report shows an anomaly; asked only when it matters
	Notify  func(string) error // delivers a notification; nil: none
	Log     core.Logger        // nil: the store's log
}

// RunOpts are the flags of `agent run`.
type RunOpts struct {
	Trigger string
	Force   bool // skips the timing, never the opt-in
}

// RunResult says what a run did. Ran is false when the timing said not yet.
type RunResult struct {
	Ran     bool
	Reason  string
	Saved   []string
	Dropped int
	CostUSD float64
}

type lastRun struct {
	T time.Time `json:"t"`
}

func lastPath(p config.Paths) string { return filepath.Join(p.State, "agent", "last.json") }

func readLast(p config.Paths) (time.Time, bool) {
	b, err := os.ReadFile(lastPath(p))
	if err != nil {
		return time.Time{}, false
	}
	var l lastRun
	if json.Unmarshal(b, &l) != nil || l.T.IsZero() {
		return time.Time{}, false
	}
	return l.T, true
}

// Run starts the agent when it is enabled, the CLI is usable and the timing allows it, then validates and saves
// its proposals. An error means the agent did not run or its run failed; the cause is logged.
func Run(d RunDeps, o RunOpts) (RunResult, error) {
	log := d.Log
	if log == nil {
		log = d.Store.Logger()
	}
	if !d.Cfg.Agent.Enabled {
		return RunResult{}, ErrDisabled
	}
	if d.Store.Paused() { // sapulidi pause stops every run, before anything is spent
		return RunResult{Reason: "paused"}, nil
	}
	cli := d.Cfg.Agent.CLI
	if cli == "" {
		cli = "claude"
	}
	argv, err := Args(cli, d.Bin, d.Cfg.Agent.MaxUSD)
	if err != nil {
		return RunResult{}, err
	}
	if _, err := d.Runner.LookPath(cli); err != nil {
		return RunResult{}, fmt.Errorf("%s not found on PATH", cli)
	}

	if !o.Force {
		if last, ok := readLast(d.Paths); ok {
			interval := d.Cfg.Agent.Interval.D()
			if interval <= 0 {
				interval = defaultInterval
			}
			el := d.Now.Sub(last)
			due := el >= interval
			if !due && el >= anomalyInterval && d.Anomaly != nil && d.Anomaly() {
				due = true
			}
			if !due {
				return RunResult{Reason: "not due yet"}, nil
			}
		}
	}

	// The attempt counts even when it fails, so a broken login does not retry on every scheduled tick.
	agentDir := filepath.Join(d.Paths.State, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return RunResult{}, err
	}
	b, _ := json.Marshal(lastRun{T: d.Now.UTC()})
	if err := atomic.WriteFile(lastPath(d.Paths), append(b, '\n')); err != nil {
		return RunResult{}, err
	}
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return RunResult{}, err
	}
	// The random part keeps two runs in the same second (and their proposal files) apart.
	runID := d.Now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(rnd[:])
	work := filepath.Join(agentDir, runID)
	if err := os.MkdirAll(work, 0o700); err != nil {
		return RunResult{}, err
	}
	defer os.Remove(work) // empty unless the agent wrote something; then it stays for inspection

	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	out, runErr := d.Runner.Run(ctx, work, argv...)
	res := RunResult{Ran: true}
	co, perr := parseOutput(out)
	res.CostUSD = co.CostUSD
	if runErr != nil || perr != nil || co.IsError {
		why := co.Subtype
		switch {
		case why != "":
		case runErr != nil:
			why = runErr.Error()
		case perr != nil:
			why = perr.Error()
		default:
			why = "is_error"
		}
		log.Event("agent-failed", map[string]any{"trigger": o.Trigger, "subtype": co.Subtype, "error": why, "cost_usd": co.CostUSD})
		return res, fmt.Errorf("agent did not run: %s", why)
	}

	if len(co.Result) > MaxResult {
		log.Event("agent-output-discarded", map[string]any{"trigger": o.Trigger, "error": "answer too long", "bytes": len(co.Result), "cost_usd": co.CostUSD})
		return res, fmt.Errorf("agent output discarded: the answer is too long (%d bytes, at most %d)", len(co.Result), MaxResult)
	}
	props, more, err := parseProposals(co.Result)
	if err != nil {
		log.Event("agent-output-discarded", map[string]any{"trigger": o.Trigger, "error": err.Error(), "cost_usd": co.CostUSD})
		return res, fmt.Errorf("agent output discarded: %v", err)
	}
	if more {
		res.Dropped++ // the elements past the fifth are never read
		log.Event("proposal-dropped", map[string]any{"error": "more than 5 proposals"})
	}
	dir := d.Store.ProposalsDir()
	for i, p := range props {
		p.ID = fmt.Sprintf("%s-%d", runID, i+1) // ids are ours: the agent's text never names a file
		if err := Validate(p); err != nil {
			res.Dropped++
			log.Event("proposal-dropped", map[string]any{"error": clip(err.Error())})
			continue
		}
		if err := Save(dir, p); err != nil {
			return res, err
		}
		res.Saved = append(res.Saved, p.ID)
	}
	log.Event("agent-run", map[string]any{"trigger": o.Trigger, "saved": len(res.Saved), "dropped": res.Dropped, "cost_usd": co.CostUSD})

	if len(res.Saved) > 0 {
		all, _ := List(dir)
		text := fmt.Sprintf("%d proposals", len(all))
		if d.Store.Fact("proposals", text, true) && d.Notify != nil {
			if err := d.Notify(text); err != nil {
				d.Store.MarkUndelivered("proposals")
			}
		}
	}
	return res, nil
}

func clip(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
