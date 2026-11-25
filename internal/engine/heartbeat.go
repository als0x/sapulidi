package engine

import (
	"os"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/delivery"
	"github.com/als0x/sapulidi/internal/state"
)

const (
	// heartbeatMaxAge is how long the newest host heartbeat may be before a scheduled run stops deleting (spec 4.1).
	heartbeatMaxAge = 30 * 24 * time.Hour
	// heartbeatSkew is how far into the future a beat's time may be and still count.
	heartbeatSkew   = 5 * time.Minute
	uninstalledText = "sapulidi seems uninstalled; run sapulidi uninstall or open a session"
)

func init() { SetBeforeRun(heartbeatHook) }

// heartbeatHook is the BeforeRun hook. A scheduled run that finds no heartbeat newer than 30 days — and no
// standalone install.sh marker — is forced to Mode "uninstalled" (everything dry run) and tells the user once.
// Otherwise, on a real run, it removes old unused binary versions at most once a day.
func heartbeatHook(env *core.Env, st *state.Store, opt Options) string {
	now := env.Now
	if now.IsZero() {
		now = time.Now()
	}
	beats := st.Heartbeats()
	if opt.Trigger == "schedule" && !anyFresh(beats, now) && !st.InstalledBy("install.sh") {
		if env.Facts != nil {
			env.Facts.Fact("uninstalled", uninstalledText)
		}
		return "uninstalled"
	}
	// A run for exact paths the user picked removes nothing the confirmation did not list.
	if !opt.DryRun && len(opt.Explicit) == 0 {
		binaryGC(env, beats, now)
	}
	return ""
}

func anyFresh(beats map[string]state.Heartbeat, now time.Time) bool {
	for _, b := range beats {
		// A beat from the future (a clock set wrong, or an mtime of 2099) must not keep the schedule alive.
		if d := now.Sub(b.At); d >= -heartbeatSkew && d <= heartbeatMaxAge {
			return true
		}
	}
	return false
}

// binaryGC removes unused old binary versions; failures are logged, never returned.
func binaryGC(env *core.Env, beats map[string]state.Heartbeat, now time.Time) {
	p, err := config.ResolvePaths()
	if err != nil {
		return
	}
	named := make([]string, 0, len(beats))
	for _, b := range beats {
		named = append(named, b.Version)
	}
	exe, _ := os.Executable()
	removed, ran, err := delivery.GCDaily(p.State, delivery.GCOptions{Data: p.Data, Home: env.Home, Now: now, Named: named, Running: exe})
	if env.Log == nil || (!ran && err == nil) {
		return
	}
	if err != nil {
		env.Log.Event("gc-error", map[string]any{"error": err.Error()})
	}
	if len(removed) > 0 {
		env.Log.Event("gc", map[string]any{"removed": removed})
	}
}
