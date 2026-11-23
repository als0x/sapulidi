// Package containers reports Docker and Podman disk use and proposes three small prunes (dangling images, old
// Docker build cache, exited containers without mounts older than the category age) plus stopping an idle
// Podman machine or Colima VM (spec 3.1, rules in packs/containers.toml). Volumes are only ever reported, an
// engine that is not running is skipped and never started, and every command is built here, never taken from a
// pack. Every doubt is a keep. They propose; the engine guards and runs.
package containers

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
)

// VMIdle is how long a VM and all of its containers must have been quiet before the VM is stopped. The
// category age is not used for it.
const VMIdle = 2 * time.Hour

const (
	callTimeout = 15 * time.Second
	dockerRoot  = "/var/lib/docker"
)

var engines = []string{"docker", "podman"}

func init() {
	for _, t := range engines {
		for _, a := range []string{"image-prune", "builder-prune", "container-prune"} {
			core.RegisterToolAction(t, a)
		}
	}
	core.RegisterToolAction("podman", "machine-stop")
	core.RegisterToolAction("colima", "stop")
	core.Register(&cat{})
}

type cat struct{}

func (*cat) Name() string        { return "containers" }
func (*cat) Bucket() core.Bucket { return core.Optional }

func ruleByID(env *core.Env, id string) *core.Rule {
	for i := range env.Rules {
		if r := &env.Rules[i]; r.Category == "containers" && r.Bucket != core.Never && r.ID == id {
			return r
		}
	}
	return nil
}

func ruleByKey(env *core.Env, key string) *core.Rule {
	for i := range env.Rules {
		if r := &env.Rules[i]; r.Category == "containers" && r.Key() == key {
			return r
		}
	}
	return nil
}

// run calls an engine with the 15 s limit; an error means "unavailable".
func run(env *core.Env, argv ...string) ([]byte, error) {
	parent := env.Ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, callTimeout)
	defer cancel()
	return env.Run.Run(ctx, env.Home, argv...)
}

func have(env *core.Env, tool string) bool {
	_, err := env.Run.LookPath(tool)
	return err == nil
}

func event(env *core.Env, kind string, fields map[string]any) {
	if env.Log != nil {
		env.Log.Event(kind, fields)
	}
}

func fmtBytes(n int64) string {
	f, units := float64(n), []string{"B", "kB", "MB", "GB", "TB"}
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

func ageText(age time.Duration) string {
	if age%(24*time.Hour) == 0 {
		return strconv.Itoa(int(age/(24*time.Hour))) + " days"
	}
	return age.String()
}

// The exact argv of every command this category runs; Proof compares candidates against them. Variable parts
// come after `--` and are validated before they get here.

func imagePruneCmd(eng string) []string { return []string{eng, "image", "prune", "-f"} }
func builderPruneCmd(age time.Duration) []string {
	return []string{"docker", "builder", "prune", "-f", "--filter", "until=" + strconv.Itoa(int(age.Hours())) + "h"}
}
func rmCmd(eng, id string) []string { return []string{eng, "rm", "--", id} }

func danglingImages(env *core.Env, eng string) bool {
	out, err := run(env, eng, "images", "-q", "--filter", "dangling=true")
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func listContainers(env *core.Env, eng string, filter ...string) ([]ctr, error) {
	argv := append([]string{eng, "ps", "-a"}, filter...)
	out, err := run(env, append(argv, "--format", "json")...)
	if err != nil {
		return nil, err
	}
	return parseContainers(out)
}

func dfUsage(env *core.Env, eng string) ([]usage, error) {
	out, err := run(env, eng, "system", "df", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parseDf(out)
}

// rootfulDocker fails closed: on Linux, anything but a confirmed user-owned /var/lib/docker is rootful.
func rootfulDocker(env *core.Env) bool {
	if env.Platform.OS() != "linux" {
		return false
	}
	owned, err := env.Platform.OwnedByUser(dockerRoot)
	return err != nil || !owned
}

func (*cat) Scan(env *core.Env) ([]core.Candidate, []core.Kept, error) {
	var cands []core.Candidate
	var kept []core.Kept
	for _, eng := range engines {
		if env.Ctx != nil && env.Ctx.Err() != nil {
			break
		}
		if have(env, eng) {
			c, k := scanEngine(env, eng)
			cands, kept = append(cands, c...), append(kept, k...)
		}
	}
	if env.Platform.OS() == "darwin" && ruleByID(env, "vm-idle") != nil {
		c, k := scanVMs(env)
		cands, kept = append(cands, c...), append(kept, k...)
	}
	return cands, kept, nil
}

func scanEngine(env *core.Env, eng string) (cands []core.Candidate, kept []core.Kept) {
	rows, err := dfUsage(env, eng)
	if err != nil {
		code := -1
		var ee *execx.ExitError
		if errors.As(err, &ee) {
			code = ee.Code
		}
		event(env, "engine-unavailable", map[string]any{"engine": eng, "reason": "system df failed or unreadable", "exit_code": code})
		return nil, nil
	}
	imgRule, bldRule, ctrRule := ruleByID(env, "image-prune"), ruleByID(env, "builder-prune"), ruleByID(env, "container-prune")
	var anyRule *core.Rule
	for _, r := range []*core.Rule{imgRule, bldRule, ctrRule, ruleByID(env, "vm-idle")} {
		if r != nil {
			anyRule = r
			break
		}
	}
	if anyRule == nil {
		return nil, nil
	}
	var buildReclaimable int64
	for _, u := range rows {
		cands = append(cands, core.Candidate{
			Category: "containers", RuleKey: anyRule.Key(), Removal: core.ReportOnly, Bytes: u.size,
			Reason: fmt.Sprintf("%s %s: %s, %s reclaimable", eng, u.label, fmtBytes(u.size), fmtBytes(u.reclaimable)),
		})
		if u.label == "build cache" {
			buildReclaimable = u.reclaimable
		}
	}
	if eng == "docker" && rootfulDocker(env) {
		return append(cands, core.Candidate{
			Category: "containers", RuleKey: anyRule.Key(), Removal: core.ReportOnly, Bytes: -1,
			Reason: "docker runs as root (" + dockerRoot + " is not confirmed yours): report only",
		}), nil
	}

	if imgRule != nil && danglingImages(env, eng) {
		cands = append(cands, core.Candidate{
			Category: "containers", RuleKey: imgRule.Key(), Removal: core.ToolCommand, Command: imagePruneCmd(eng),
			Reason: eng + ": dangling images (never image prune -a)",
		})
	}
	if bldRule != nil && eng == "docker" && buildReclaimable > 0 {
		age := env.Settings.Age("containers", bldRule.MinAge)
		cands = append(cands, core.Candidate{
			Category: "containers", RuleKey: bldRule.Key(), Removal: core.ToolCommand, Command: builderPruneCmd(age),
			Reason: "docker: build cache older than " + ageText(age),
		})
	}
	if ctrRule != nil {
		c, k := scanContainers(env, eng, ctrRule)
		cands, kept = append(cands, c...), k
	}
	return cands, kept
}

// containerBlocker is the one place that decides whether an exited container may be removed.
func containerBlocker(env *core.Env, c ctr, age time.Duration) error {
	switch {
	case c.State != "exited":
		return core.Keep("active", "container is not exited")
	case !idRe.MatchString(c.ID):
		return core.Keep("error", "unexpected container id")
	case !c.mountsEmpty():
		return core.Keep("active", "container has mounts, or its mounts are unknown")
	case c.ExitedAt <= 0:
		return core.Keep("young", "exit time unknown")
	case env.Now.Sub(secs(c.ExitedAt)) <= age:
		return core.Keep("young", "exited less than "+age.String()+" ago")
	}
	return nil
}

func scanContainers(env *core.Env, eng string, rule *core.Rule) (cands []core.Candidate, kept []core.Kept) {
	cs, err := listContainers(env, eng, "--filter", "status=exited")
	if err != nil {
		return nil, nil
	}
	age := env.Settings.Age("containers", rule.MinAge)
	for _, c := range cs {
		if c.State != "exited" {
			continue
		}
		if err := containerBlocker(env, c, age); err != nil {
			kept = append(kept, core.Kept{Category: "containers", RuleKey: rule.Key(), Path: eng + ":" + c.ID, Why: err.(*core.KeepError).Why})
			continue
		}
		cands = append(cands, core.Candidate{
			Category: "containers", RuleKey: rule.Key(), Removal: core.ToolCommand, Command: rmCmd(eng, c.ID),
			Reason: fmt.Sprintf("%s: container %s exited %d days ago, no mounts", eng, shortID(c.ID), int(env.Now.Sub(secs(c.ExitedAt))/(24*time.Hour))),
		})
	}
	return cands, kept
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// ---- idle VMs (macOS) ----

type vm struct {
	engine string    // the CLI that talks to the VM's engine
	label  string    // for messages
	since  time.Time // the VM's own recorded start; zero when unknown or unparsable
	stop   []string
}

type machineRow struct {
	Name    string
	Default bool
	Running bool
	LastUp  string
}

type colimaRow struct{ Name, Status string }

// listVMs returns the running VMs this category may judge, and notes for running VMs it cannot judge (shown
// report-only). A stopped VM is never started. Podman: only the default machine, because `podman ps` talks to
// that one. Colima: only a single profile whose docker context is the Colima one, otherwise `docker ps` would
// answer for another engine.
func listVMs(env *core.Env) (vms []vm, notes []string) {
	if have(env, "podman") {
		if out, err := run(env, "podman", "machine", "list", "--format", "json"); err == nil {
			rows, _ := decodeList[machineRow](out)
			for _, r := range rows {
				if !r.Default || !r.Running {
					continue
				}
				name := strings.TrimSuffix(r.Name, "*")
				if !nameRe.MatchString(name) {
					notes = append(notes, "podman machine with an unexpected name: report only")
					continue
				}
				since, _ := time.Parse(time.RFC3339, r.LastUp)
				vms = append(vms, vm{engine: "podman", label: "podman machine " + name, since: since,
					stop: []string{"podman", "machine", "stop", "--", name}})
			}
		}
	}
	if have(env, "colima") && have(env, "docker") {
		out, err := run(env, "colima", "list", "--json")
		rows, perr := decodeList[colimaRow](out)
		switch {
		case err != nil || perr != nil || len(rows) == 0:
		case len(rows) != 1:
			notes = append(notes, fmt.Sprintf("colima has %d profiles: report only", len(rows)))
		case rows[0].Status == "Running":
			r := rows[0]
			ctx, cerr := run(env, "docker", "context", "show")
			got := strings.TrimSpace(string(ctx))
			switch {
			case !nameRe.MatchString(r.Name):
				notes = append(notes, "colima profile with an unexpected name: report only")
			case cerr != nil || (got != "colima" && got != "colima-"+r.Name):
				notes = append(notes, "colima is running but the docker context is not colima's: report only")
			default:
				stop := []string{"colima", "stop"}
				if r.Name != "default" {
					stop = append(stop, "--", r.Name)
				}
				// colima list has no start time, so the VM is kept (see vmIdle).
				vms = append(vms, vm{engine: "docker", label: "colima " + r.Name, stop: stop})
			}
		}
	}
	return vms, notes
}

func scanVMs(env *core.Env) (cands []core.Candidate, kept []core.Kept) {
	rule := ruleByID(env, "vm-idle")
	vms, notes := listVMs(env)
	for _, n := range notes {
		cands = append(cands, core.Candidate{Category: "containers", RuleKey: rule.Key(), Removal: core.ReportOnly, Bytes: -1, Reason: n})
	}
	for _, v := range vms {
		idle, err := vmIdle(env, v)
		if err != nil {
			kept = append(kept, core.Kept{Category: "containers", RuleKey: rule.Key(), Path: v.label, Why: err.(*core.KeepError).Why})
			continue
		}
		cands = append(cands, core.Candidate{
			Category: "containers", RuleKey: rule.Key(), Removal: core.ToolCommand, Command: v.stop,
			Reason: fmt.Sprintf("%s is idle: no activity for %s, no container running, no connection, build or compose process", v.label, idle.Truncate(time.Minute)),
		})
	}
	return cands, kept
}

// vmIdle returns how long the VM has been quiet, or a *core.KeepError. The VM is stopped only when its own
// start is known and old, no container runs or is in any state but exited or created, nothing started, exited
// or was created within VMIdle, no published port has an established connection and no build runs. Every
// doubt (an unreadable answer, a missing time, lsof or ps failing) is a keep.
func vmIdle(env *core.Env, v vm) (time.Duration, error) {
	if v.since.IsZero() {
		return 0, core.Keep("young", "start time of the VM is unknown")
	}
	cs, err := listContainers(env, v.engine)
	if err != nil {
		return 0, core.Keep("error", "container list failed: "+err.Error())
	}
	last := v.since
	var ports []int
	for _, c := range cs {
		if c.State != "exited" && c.State != "created" {
			return 0, core.Keep("active", fmt.Sprintf("container %s is %q", shortID(c.ID), c.State))
		}
		created, ok := c.createdTime()
		if !ok {
			return 0, core.Keep("young", "creation time of container "+shortID(c.ID)+" unknown")
		}
		times := []time.Time{created}
		if c.StartedAt > 0 {
			times = append(times, secs(c.StartedAt))
		}
		if c.State == "exited" {
			if c.ExitedAt <= 0 {
				return 0, core.Keep("young", "exit time of container "+shortID(c.ID)+" unknown")
			}
			times = append(times, secs(c.ExitedAt))
		}
		for _, t := range times {
			if t.After(last) {
				last = t
			}
		}
		ps, ok := c.hostPorts()
		if !ok {
			return 0, core.Keep("error", "published ports of container "+shortID(c.ID)+" unreadable")
		}
		ports = append(ports, ps...)
	}
	idle := env.Now.Sub(last)
	if idle < VMIdle {
		return 0, core.Keep("young", "activity "+idle.Truncate(time.Minute).String()+" ago")
	}
	out, err := run(env, "ps", "-axo", "command")
	if err != nil {
		return 0, core.Keep("error", "process list failed: "+err.Error())
	}
	if buildActive(string(out)) {
		return 0, core.Keep("process", "a build, pull or compose process is running")
	}
	if len(ports) > 0 && !have(env, "lsof") {
		return 0, core.Keep("error", "lsof not found")
	}
	for _, p := range ports {
		out, err := run(env, "lsof", "-nP", "-iTCP:"+strconv.Itoa(p), "-sTCP:ESTABLISHED")
		var ee *execx.ExitError
		switch {
		case err == nil && strings.TrimSpace(string(out)) != "":
			return 0, core.Keep("open", "established connection on port "+strconv.Itoa(p))
		case err == nil || errors.As(err, &ee) && ee.Code == 1 && strings.TrimSpace(string(out)) == "":
		default:
			return 0, core.Keep("error", "lsof failed: "+err.Error())
		}
	}
	return idle, nil
}

var buildArgs = []string{"build", "buildx", "bake", "pull", "compose", "bud", "push"}

// buildActive reports a container tool process with a build-like word anywhere in its arguments.
func buildActive(ps string) bool {
	for line := range strings.SplitSeq(ps, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch filepath.Base(f[0]) {
		case "docker-compose", "podman-compose":
			return true
		case "docker", "podman", "nerdctl", "buildkitd", "colima", "buildah":
			for _, a := range f[1:] {
				if slices.Contains(buildArgs, a) {
					return true
				}
			}
		}
	}
	return false
}

// ---- proof ----

func buildCacheReclaimable(env *core.Env) bool {
	rows, err := dfUsage(env, "docker")
	if err != nil {
		return false
	}
	for _, u := range rows {
		if u.label == "build cache" && u.reclaimable > 0 {
			return true
		}
	}
	return false
}

// Proof re-queries the engine right before the command runs (spec S8) and rebuilds the argv from what it
// found; the candidate's command must equal it.
func (*cat) Proof(env *core.Env, c core.Candidate) error {
	rule := ruleByKey(env, c.RuleKey)
	if rule == nil || len(c.Command) < 2 {
		return core.Keep("error", "no containers rule or command")
	}
	eng := c.Command[0]
	if eng == "docker" && rootfulDocker(env) {
		return core.Keep("error", "docker runs as root")
	}
	switch rule.ID {
	case "image-prune":
		if !slices.Contains(engines, eng) || !slices.Equal(c.Command, imagePruneCmd(eng)) {
			return core.Keep("error", "unexpected command")
		}
		if !danglingImages(env, eng) {
			return core.Keep("no-marker", "no dangling image left")
		}
	case "builder-prune":
		if !slices.Equal(c.Command, builderPruneCmd(env.Settings.Age("containers", rule.MinAge))) {
			return core.Keep("error", "unexpected command")
		}
		if !buildCacheReclaimable(env) {
			return core.Keep("no-marker", "no reclaimable build cache left")
		}
	case "container-prune":
		if len(c.Command) != 4 || !slices.Contains(engines, eng) || !idRe.MatchString(c.Command[3]) {
			return core.Keep("error", "unexpected command")
		}
		cs, err := listContainers(env, eng, "--filter", "status=exited")
		if err != nil {
			return core.Keep("error", "container list failed: "+err.Error())
		}
		i := slices.IndexFunc(cs, func(x ctr) bool { return x.ID == c.Command[3] })
		if i < 0 {
			return core.Keep("no-marker", "container is gone or no longer exited")
		}
		if err := containerBlocker(env, cs[i], env.Settings.Age("containers", rule.MinAge)); err != nil {
			return err
		}
		if !slices.Equal(c.Command, rmCmd(eng, cs[i].ID)) {
			return core.Keep("error", "unexpected command")
		}
	case "vm-idle":
		vms, _ := listVMs(env)
		for _, v := range vms {
			if slices.Equal(v.stop, c.Command) {
				_, err := vmIdle(env, v)
				return err
			}
		}
		return core.Keep("no-marker", "VM is not running or not judgeable")
	default:
		return core.Keep("error", "rule "+rule.ID+" has no proof")
	}
	return nil
}
