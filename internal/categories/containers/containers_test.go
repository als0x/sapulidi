package containers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/als0x/sapulidi/internal/core"
	"github.com/als0x/sapulidi/internal/execx"
	packsload "github.com/als0x/sapulidi/internal/packs"
	"github.com/als0x/sapulidi/internal/testutil"
	"github.com/als0x/sapulidi/packs"
)

// now is the fixed clock the ps fixture was written for (see testdata/README.md).
var now = time.Unix(1760000000, 0)

// loadRules loads only the containers pack, never the whole embedded set. The tests pretend to be darwin (see
// podmanEnv), so the rules are loaded for darwin whatever OS the test runs on.
func loadRules(t *testing.T) []core.Rule {
	t.Helper()
	data, err := packs.FS.ReadFile("containers.toml")
	if err != nil {
		t.Fatal(err)
	}
	rules, errs := packsload.LoadFor(fstest.MapFS{"containers.toml": {Data: data}}, "", "darwin")
	if len(errs) > 0 {
		t.Fatalf("pack errors: %v", errs)
	}
	return rules
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type env struct {
	*core.Env
	run  *testutil.FakeRunner
	plat *testutil.FakePlatform
}

// podmanEnv is a darwin machine with podman and lsof installed and a running, idle podman machine.
func podmanEnv(t *testing.T) *env {
	t.Helper()
	t.Setenv("SAPULIDI_TEST", "1")
	home := testutil.TempHome(t)
	run := &testutil.FakeRunner{Paths: map[string]string{"podman": "/bin/podman", "lsof": "/bin/lsof", "ps": "/bin/ps"}}
	run.On([]string{"podman", "system", "df"}, fixture(t, "podman-df.json"), nil)
	run.On([]string{"podman", "ps", "-a"}, fixture(t, "podman-ps-a.json"), nil)
	run.On([]string{"podman", "images"}, "sha256:aaa\n", nil)
	run.On([]string{"podman", "machine", "list"}, fixture(t, "podman-machine-list.json"), nil)
	run.On([]string{"lsof"}, "", &execx.ExitError{Code: 1})
	run.On([]string{"ps", "-axo"}, "/usr/bin/vim a.txt\n", nil)
	plat := &testutil.FakePlatform{OSName: "darwin", Temp: filepath.Join(home, "tmp")}
	e := &core.Env{
		Home: home, Now: now, Rules: loadRules(t), Platform: plat, Run: run,
		Settings: &testutil.FakeSettings{ConsentAll: true, Categories: map[string]bool{"containers": true}},
		Log:      &testutil.MemLogger{}, Facts: &testutil.MemFacts{}, Ctx: context.Background(),
	}
	return &env{Env: e, run: run, plat: plat}
}

func scan(t *testing.T, e *env) ([]core.Candidate, []core.Kept) {
	t.Helper()
	cands, kept, err := (&cat{}).Scan(e.Env)
	if err != nil {
		t.Fatalf("Scan returned an error: %v", err)
	}
	return cands, kept
}

func commands(cands []core.Candidate) [][]string {
	var out [][]string
	for _, c := range cands {
		if c.Removal == core.ToolCommand {
			out = append(out, c.Command)
		}
	}
	return out
}

func hasCommand(cands []core.Candidate, want ...string) bool {
	return slices.ContainsFunc(commands(cands), func(c []string) bool { return slices.Equal(c, want) })
}

const idOld = "0000000000000000000000000000000000000000000000000000000000abc001"

func TestDfParsedDockerAndPodman(t *testing.T) {
	e := podmanEnv(t)
	e.run.Paths["docker"] = "/bin/docker"
	e.run.On([]string{"docker", "system", "df"}, `{"Type":"Images","TotalCount":"3","Active":"1","Size":"1.5GB","Reclaimable":"1GB (66%)"}
{"Type":"Containers","TotalCount":"2","Active":"0","Size":"10MB","Reclaimable":"10MB (100%)"}
{"Type":"Local Volumes","TotalCount":"1","Active":"1","Size":"700kB","Reclaimable":"0B (0%)"}
{"Type":"Build Cache","TotalCount":"5","Active":"0","Size":"2GB","Reclaimable":"2GB"}
`, nil)
	e.run.On([]string{"docker", "ps"}, "", nil)
	e.run.On([]string{"docker", "images"}, "", nil)
	cands, _ := scan(t, e)
	size := map[string]int64{}
	for _, c := range cands {
		if c.Removal == core.ReportOnly {
			label, _, _ := strings.Cut(c.Reason, ":")
			size[label] = c.Bytes
		}
	}
	want := map[string]int64{
		"podman images": 3261813978, "podman containers": 3370812523, "podman volumes": 4983140306,
		"docker images": 1500000000, "docker containers": 10000000, "docker volumes": 700000, "docker build cache": 2000000000,
	}
	for k, v := range want {
		if size[k] != v {
			t.Errorf("%s = %d, want %d (all: %v)", k, size[k], v, size)
		}
	}
}

func TestVolumesAlwaysReportOnly(t *testing.T) {
	e := podmanEnv(t)
	cands, _ := scan(t, e)
	seen := false
	for _, c := range cands {
		if strings.Contains(c.Reason, "volumes") {
			seen = true
			if c.Removal != core.ReportOnly {
				t.Errorf("volumes candidate has removal %s", c.Removal)
			}
		}
	}
	if !seen {
		t.Error("volumes are not reported")
	}
	for _, argv := range commands(cands) {
		if slices.Contains(argv, "volume") || slices.Contains(argv, "--volumes") {
			t.Errorf("command touches volumes: %v", argv)
		}
	}
}

func TestEngineDownIsUnavailableNotError(t *testing.T) {
	e := podmanEnv(t)
	e.run.On([]string{"podman", "system", "df"}, "", errors.New("cannot connect to podman"))
	cands, kept := scan(t, e)
	for _, c := range cands {
		if c.Removal == core.ToolCommand && c.Command[0] == "podman" && c.Command[1] != "machine" {
			t.Errorf("candidate for an unavailable engine: %v", c.Command)
		}
	}
	_ = kept

	e = podmanEnv(t)
	e.run.On([]string{"podman", "system", "df"}, "not json at all", nil)
	cands, _ = scan(t, e)
	for _, c := range cands {
		if c.Removal == core.ToolCommand && c.Command[1] != "machine" {
			t.Errorf("garbled df output must mean unavailable, got candidate %v", c.Command)
		}
	}
}

func TestImagePruneNeverAll(t *testing.T) {
	e := podmanEnv(t)
	cands, _ := scan(t, e)
	if !hasCommand(cands, "podman", "image", "prune", "-f") {
		t.Fatalf("no image-prune candidate: %v", commands(cands))
	}
	for _, argv := range commands(cands) {
		if slices.Contains(argv, "-a") || slices.Contains(argv, "--all") {
			t.Errorf("command prunes everything: %v", argv)
		}
	}
	// the dangling check is the proof: nothing dangling any more means keep
	var img core.Candidate
	for _, c := range cands {
		if slices.Equal(c.Command, []string{"podman", "image", "prune", "-f"}) {
			img = c
		}
	}
	if err := (&cat{}).Proof(e.Env, img); err != nil {
		t.Errorf("proof with dangling images: %v", err)
	}
	e.run.On([]string{"podman", "images"}, "", nil)
	if err := (&cat{}).Proof(e.Env, img); err == nil {
		t.Error("proof must keep when no image is dangling any more")
	}
}

func TestContainerWithMountKept(t *testing.T) {
	e := podmanEnv(t)
	cands, kept := scan(t, e)
	if hasCommand(cands, "podman", "rm", "--", "0000000000000000000000000000000000000000000000000000000000abc003") {
		t.Error("container with a mount is a candidate")
	}
	if !slices.ContainsFunc(kept, func(k core.Kept) bool { return strings.HasSuffix(k.Path, "abc003") }) {
		t.Errorf("container with a mount is not listed as kept: %v", kept)
	}
}

func TestContainerRecentKept(t *testing.T) {
	e := podmanEnv(t)
	cands, _ := scan(t, e)
	if hasCommand(cands, "podman", "rm", "--", "0000000000000000000000000000000000000000000000000000000000abc002") {
		t.Error("container that exited 2 days ago is a candidate")
	}
	if hasCommand(cands, "podman", "rm", "--", "0000000000000000000000000000000000000000000000000000000000abc004") {
		t.Error("running container is a candidate")
	}
}

func TestOneCandidatePerContainer(t *testing.T) {
	e := podmanEnv(t)
	cands, _ := scan(t, e)
	n := 0
	for _, argv := range commands(cands) {
		if len(argv) > 1 && argv[1] == "rm" {
			n++
			if !slices.Equal(argv, []string{"podman", "rm", "--", idOld}) {
				t.Errorf("unexpected rm: %v", argv)
			}
		}
		if slices.Contains(argv, "container") && slices.Contains(argv, "prune") {
			t.Errorf("bulk container prune used: %v", argv)
		}
	}
	if n != 1 {
		t.Errorf("rm candidates = %d, want 1", n)
	}
	for _, c := range cands {
		if c.Removal == core.ToolCommand && (c.Path != "" || c.Base != "") {
			t.Errorf("tool command with a path: %+v", c)
		}
	}
	var rm core.Candidate
	for _, c := range cands {
		if len(c.Command) > 1 && c.Command[1] == "rm" {
			rm = c
		}
	}
	if err := (&cat{}).Proof(e.Env, rm); err != nil {
		t.Errorf("proof: %v", err)
	}
	// the container got a mount (or was started again) after the scan
	e.Now = time.Unix(1756112000+3600, 0)
	if err := (&cat{}).Proof(e.Env, rm); err == nil {
		t.Error("proof must keep a container that is no longer old enough")
	}
}

func TestMountsMustBeExactlyEmpty(t *testing.T) {
	old := func(mounts string) string {
		return `[{"Id":"` + idOld + `","State":"exited","ExitedAt":1756112000,"Created":1700000000` + mounts + `}]`
	}
	for name, body := range map[string]string{
		"missing": old(""), "null": old(`,"Mounts":null`), "object": old(`,"Mounts":{"a":1}`),
		"string": old(`,"Mounts":"vol1"`), "list": old(`,"Mounts":["/data"]`), "number": old(`,"Mounts":3`),
	} {
		e := podmanEnv(t)
		e.run.On([]string{"podman", "ps", "-a"}, body, nil)
		cands, _ := scan(t, e)
		if hasCommand(cands, "podman", "rm", "--", idOld) {
			t.Errorf("mounts %s: container is a candidate", name)
		}
	}
	for name, mounts := range map[string]string{"empty list": `,"Mounts":[]`, "empty string": `,"Mounts":""`} {
		e := podmanEnv(t)
		e.run.On([]string{"podman", "ps", "-a"}, old(mounts), nil)
		cands, _ := scan(t, e)
		if !hasCommand(cands, "podman", "rm", "--", idOld) {
			t.Errorf("mounts %s: container is not a candidate", name)
		}
	}
}

func TestExitedIsStrict(t *testing.T) {
	e := podmanEnv(t)
	e.run.On([]string{"podman", "ps", "-a"}, `[{"Id":"`+idOld+`","State":"stopped","Exited":true,"ExitedAt":1756112000,"Mounts":[]}]`, nil)
	cands, _ := scan(t, e)
	if hasCommand(cands, "podman", "rm", "--", idOld) {
		t.Error("Exited:true with another State must not be removable")
	}
}

func TestContainerIDMustBeHex(t *testing.T) {
	for _, id := range []string{"--all", "ABCDEF0123456789", "abc", "abcdef012345; x", "../abcdef012345", strings.Repeat("a", 65)} {
		e := podmanEnv(t)
		e.run.On([]string{"podman", "ps", "-a"}, `[{"Id":"`+id+`","State":"exited","ExitedAt":1756112000,"Mounts":[]}]`, nil)
		cands, kept := scan(t, e)
		for _, c := range commands(cands) {
			if len(c) > 1 && c[1] == "rm" {
				t.Errorf("id %q became a candidate: %v", id, c)
			}
		}
		if len(kept) == 0 {
			t.Errorf("id %q is not reported as kept", id)
		}
		if err := (&cat{}).Proof(e.Env, core.Candidate{Category: "containers", RuleKey: "containers/container-prune@1", Removal: core.ToolCommand, Command: []string{"podman", "rm", "--", id}}); err == nil {
			t.Errorf("proof accepted id %q", id)
		}
	}
}

func TestProofRequeriesContainer(t *testing.T) {
	e := podmanEnv(t)
	cands, _ := scan(t, e)
	var rm core.Candidate
	for _, c := range cands {
		if len(c.Command) > 1 && c.Command[1] == "rm" {
			rm = c
		}
	}
	if rm.Command == nil {
		t.Fatal("no rm candidate")
	}
	for name, body := range map[string]string{
		"gone":      `[]`,
		"remounted": `[{"Id":"` + idOld + `","State":"exited","ExitedAt":1756112000,"Mounts":["/data"]}]`,
		"restarted": `[{"Id":"` + idOld + `","State":"running","Mounts":[]}]`,
		"young":     `[{"Id":"` + idOld + `","State":"exited","ExitedAt":1759999000,"Mounts":[]}]`,
	} {
		e.run.On([]string{"podman", "ps", "-a"}, body, nil)
		if err := (&cat{}).Proof(e.Env, rm); err == nil {
			t.Errorf("proof must keep a container that is %s", name)
		}
	}
	// a command that is not the rebuilt one is refused
	e.run.On([]string{"podman", "ps", "-a"}, fixture(t, "podman-ps-a.json"), nil)
	bad := rm
	bad.Command = []string{"podman", "rm", "-f", idOld}
	if err := (&cat{}).Proof(e.Env, bad); err == nil {
		t.Error("proof accepted a foreign command")
	}
}

func dockerEnv(t *testing.T) *env {
	t.Helper()
	e := podmanEnv(t)
	delete(e.run.Paths, "podman")
	e.run.Paths["docker"] = "/bin/docker"
	e.plat.OSName = "darwin"
	e.run.On([]string{"docker", "system", "df"}, `{"Type":"Images","Size":"1GB","Reclaimable":"1GB"}
{"Type":"Build Cache","Size":"2GB","Reclaimable":"1.5GB"}
`, nil)
	e.run.On([]string{"docker", "images"}, "sha256:aaa\n", nil)
	e.run.On([]string{"docker", "ps"}, `{"ID":"abcdef012345","State":"exited","Mounts":"","CreatedAt":"2023-11-14 22:13:20 +0000 UTC","Ports":""}`+"\n", nil)
	return e
}

func TestDockerPsShapes(t *testing.T) {
	e := dockerEnv(t)
	cands, kept := scan(t, e)
	if hasCommand(cands, "docker", "rm", "--", "abcdef012345") {
		t.Error("docker container without an exit time must be kept")
	}
	if !slices.ContainsFunc(kept, func(k core.Kept) bool { return k.Why == "young" }) {
		t.Errorf("not kept as young: %v", kept)
	}
	// unexpected shape: no candidate and no error
	for _, body := range []string{"not json", `{"ID":"abcdef012345","State":"exited","ExitedAt":"yesterday"}`, `[1,2]`} {
		e.run.On([]string{"docker", "ps"}, body, nil)
		cands, _ = scan(t, e)
		for _, c := range commands(cands) {
			if c[1] == "rm" {
				t.Errorf("shape %q produced %v", body, c)
			}
		}
	}
}

func TestBuilderPruneUsesRuleAgeAndRequeries(t *testing.T) {
	e := dockerEnv(t)
	e.plat.OSName = "darwin"
	cands, _ := scan(t, e)
	want := []string{"docker", "builder", "prune", "-f", "--filter", "until=720h"}
	if !hasCommand(cands, want...) {
		t.Fatalf("no builder prune: %v", commands(cands))
	}
	var b core.Candidate
	for _, c := range cands {
		if slices.Equal(c.Command, want) {
			b = c
		}
	}
	if !strings.Contains(b.Reason, "30 days") {
		t.Errorf("reason does not say the age: %q", b.Reason)
	}
	if err := (&cat{}).Proof(e.Env, b); err != nil {
		t.Errorf("proof: %v", err)
	}
	e.run.On([]string{"docker", "system", "df"}, `{"Type":"Build Cache","Size":"2GB","Reclaimable":"0B"}`+"\n", nil)
	if err := (&cat{}).Proof(e.Env, b); err == nil {
		t.Error("proof must re-query the build cache")
	}
	// the configured age moves the filter
	e = dockerEnv(t)
	e.Settings = &testutil.FakeSettings{ConsentAll: true, Ages: map[string]time.Duration{"containers": 10 * 24 * time.Hour}}
	cands, _ = scan(t, e)
	if !hasCommand(cands, "docker", "builder", "prune", "-f", "--filter", "until=240h") {
		t.Errorf("until does not follow the age: %v", commands(cands))
	}
}

func TestRootfulFailsClosedOnOwnerError(t *testing.T) {
	e := dockerEnv(t)
	e.plat.OSName = "linux"
	e.plat.OwnedErr = errors.New("stat failed")
	cands, _ := scan(t, e)
	if len(cands) == 0 || len(commands(cands)) != 0 {
		t.Errorf("owner error must be report only: %v", commands(cands))
	}
}

func TestEngineUnavailableLogHasNoStderr(t *testing.T) {
	e := podmanEnv(t)
	e.run.On([]string{"podman", "system", "df"}, "", &execx.ExitError{Code: 125, Stderr: "SECRET-TOKEN-in-stderr"})
	scan(t, e)
	for _, ev := range e.Log.(*testutil.MemLogger).Events {
		if fmt.Sprint(ev.Fields) != "" && strings.Contains(fmt.Sprint(ev.Fields), "SECRET") {
			t.Errorf("stderr leaked into the log: %v", ev.Fields)
		}
		if ev.Kind == "engine-unavailable" && ev.Fields["exit_code"] != 125 {
			t.Errorf("exit code missing: %v", ev.Fields)
		}
	}
}

func TestVolumesReportedWithOnlyVMRule(t *testing.T) {
	e := podmanEnv(t)
	var only []core.Rule
	for _, r := range e.Rules {
		if r.ID == "vm-idle" {
			only = append(only, r)
		}
	}
	e.Rules = only
	cands, _ := scan(t, e)
	if !slices.ContainsFunc(cands, func(c core.Candidate) bool { return strings.Contains(c.Reason, "podman volumes") }) {
		t.Error("volumes not reported with only vm-idle enabled")
	}
}

func TestColimaNeedsSingleProfileAndContext(t *testing.T) {
	setup := func(list, ctx string) *env {
		e := podmanEnv(t)
		delete(e.run.Paths, "podman")
		e.run.Paths["docker"], e.run.Paths["colima"] = "/bin/docker", "/bin/colima"
		e.run.On([]string{"colima", "list"}, list, nil)
		e.run.On([]string{"docker", "context"}, ctx, nil)
		return e
	}
	run := `{"name":"default","status":"Running"}` + "\n"
	for name, e := range map[string]*env{
		"two profiles":  setup(run+`{"name":"dev","status":"Running"}`+"\n", "colima\n"),
		"other context": setup(run, "desktop-linux\n"),
		"bad name":      setup(`{"name":"a b","status":"Running"}`+"\n", "colima-a b\n"),
	} {
		cands, _ := scan(t, e)
		if slices.ContainsFunc(cands, func(c core.Candidate) bool { return len(c.Command) > 0 && c.Command[0] == "colima" }) {
			t.Errorf("%s: colima is a candidate", name)
		}
		if !slices.ContainsFunc(cands, func(c core.Candidate) bool {
			return c.Removal == core.ReportOnly && strings.Contains(c.Reason, "colima")
		}) {
			t.Errorf("%s: colima is not reported", name)
		}
	}
	// a matching colima is judged, but colima list has no start time, so it is kept
	e := setup(run, "colima\n")
	e.run.On([]string{"docker", "ps"}, "", nil)
	e.run.On([]string{"docker", "system", "df"}, `{"Type":"Images","Size":"1GB","Reclaimable":"1GB"}`+"\n", nil)
	e.run.On([]string{"docker", "images"}, "", nil)
	cands, kept := scan(t, e)
	if slices.ContainsFunc(cands, func(c core.Candidate) bool { return len(c.Command) > 0 && c.Command[0] == "colima" }) {
		t.Error("colima without a known start time must be kept")
	}
	if len(kept) == 0 {
		t.Error("colima keep is not reported")
	}
}

func TestVMIdleStopped(t *testing.T) {
	e := podmanEnv(t)
	e.run.On([]string{"podman", "ps", "-a"}, idleCtrs, nil)
	cands, _ := scan(t, e)
	var vm *core.Candidate
	for i, c := range cands {
		if slices.Equal(c.Command, []string{"podman", "machine", "stop", "--", "podman-machine-default"}) {
			vm = &cands[i]
		}
	}
	if vm == nil {
		t.Fatalf("no machine stop candidate: %v", commands(cands))
	}
	if vm.Category != "containers" || vm.Path != "" || vm.Base != "" || !strings.Contains(vm.Reason, "idle") {
		t.Errorf("bad vm candidate: %+v", *vm)
	}
	if err := (&cat{}).Proof(e.Env, *vm); err != nil {
		t.Errorf("proof: %v", err)
	}
	if VMIdle != 2*time.Hour {
		t.Errorf("VMIdle = %s", VMIdle)
	}
}

func TestVMBusyWithConnectionKept(t *testing.T) {
	busy := func(t *testing.T, mutate func(*env)) {
		t.Helper()
		e := podmanEnv(t)
		mutate(e)
		cands, kept := scan(t, e)
		if slices.ContainsFunc(cands, func(c core.Candidate) bool { return slices.Contains(c.Command, "machine") }) {
			t.Error("busy VM is a candidate")
		}
		if len(kept) == 0 {
			t.Error("busy VM is not reported as kept")
		}
	}
	port8080 := `[{"host_ip":"","container_port":80,"host_port":8080,"range":1,"protocol":"tcp"}]`
	ps := func(e *env, body string) { e.run.On([]string{"podman", "ps", "-a"}, body, nil) }
	t.Run("connection", func(t *testing.T) {
		busy(t, func(e *env) {
			ps(e, psJSON("exited", 1756112000, 1700000100, port8080))
			e.run.On([]string{"lsof"}, "COMMAND PID\ngvproxy 1\n", nil)
		})
	})
	t.Run("connection inside a port range", func(t *testing.T) {
		busy(t, func(e *env) {
			ps(e, psJSON("exited", 1756112000, 1700000100, `[{"host_port":5432,"range":9}]`))
			e.run.On([]string{"lsof", "-nP", "-iTCP:5440"}, "COMMAND PID\ngvproxy 1\n", nil)
		})
	})
	t.Run("lsof fails", func(t *testing.T) {
		busy(t, func(e *env) {
			ps(e, psJSON("exited", 1756112000, 1700000100, port8080))
			e.run.On([]string{"lsof"}, "", errors.New("boom"))
		})
	})
	t.Run("unreadable ports", func(t *testing.T) {
		busy(t, func(e *env) { ps(e, psJSON("exited", 1756112000, 1700000100, `{"x":1}`)) })
	})
	t.Run("recent start", func(t *testing.T) {
		busy(t, func(e *env) { ps(e, psJSON("exited", 1756112000, 1759999000, `[]`)) })
	})
	t.Run("recent exit", func(t *testing.T) {
		busy(t, func(e *env) { ps(e, psJSON("exited", 1759999000, 1700000100, `[]`)) })
	})
	t.Run("running container without ports", func(t *testing.T) {
		busy(t, func(e *env) { ps(e, psJSON("running", 0, 1700000100, `[]`)) })
	})
	for _, st := range []string{"paused", "restarting"} {
		t.Run(st+" container", func(t *testing.T) {
			busy(t, func(e *env) { ps(e, psJSON(st, 0, 1700000100, `[]`)) })
		})
	}
	t.Run("fixture with a running container", func(t *testing.T) { busy(t, func(*env) {}) })
	t.Run("recent creation", func(t *testing.T) {
		busy(t, func(e *env) {
			ps(e, strings.Replace(psJSON("created", 0, 0, `[]`), "1700000000", "1759999500", 1))
		})
	})
	t.Run("VM up recently", func(t *testing.T) {
		busy(t, func(e *env) {
			ps(e, idleCtrs)
			e.run.On([]string{"podman", "machine", "list"}, strings.Replace(fixture(t, "podman-machine-list.json"), "2025-10-09T00:00:00Z", "2025-10-09T10:00:00Z", 1), nil)
		})
	})
	t.Run("LastUp empty", func(t *testing.T) {
		busy(t, func(e *env) {
			ps(e, idleCtrs)
			e.run.On([]string{"podman", "machine", "list"}, strings.Replace(fixture(t, "podman-machine-list.json"), "2025-10-09T00:00:00Z", "", 1), nil)
		})
	})
	t.Run("LastUp unparsable", func(t *testing.T) {
		busy(t, func(e *env) {
			ps(e, idleCtrs)
			e.run.On([]string{"podman", "machine", "list"}, strings.Replace(fixture(t, "podman-machine-list.json"), "2025-10-09T00:00:00Z", "yesterday", 1), nil)
		})
	})
	for _, p := range []string{
		"/usr/local/bin/docker compose up", "podman --log-level=debug build -t x .", "/usr/bin/docker buildx bake",
		"docker -H unix:///x --debug image pull alpine", "nerdctl push x", "/usr/bin/nerdctl --namespace x compose up",
	} {
		t.Run("process "+p, func(t *testing.T) {
			busy(t, func(e *env) {
				ps(e, idleCtrs)
				e.run.On([]string{"ps", "-axo"}, p+"\n", nil)
			})
		})
	}
}

// busyAtProof: the VM was idle at scan time but a connection exists when the engine runs the command.
func TestVMBusyAtProofTime(t *testing.T) {
	e := podmanEnv(t)
	e.run.On([]string{"podman", "ps", "-a"}, psJSON("exited", 1756112000, 1700000100, `[{"host_port":8080}]`), nil)
	cands, _ := scan(t, e)
	var vm core.Candidate
	for _, c := range cands {
		if slices.Contains(c.Command, "machine") {
			vm = c
		}
	}
	if vm.Command == nil {
		t.Fatalf("VM is not a candidate: %v", commands(cands))
	}
	if err := (&cat{}).Proof(e.Env, vm); err != nil {
		t.Fatalf("proof when idle: %v", err)
	}
	e.run.On([]string{"lsof"}, "COMMAND PID\ngvproxy 1\n", nil)
	if err := (&cat{}).Proof(e.Env, vm); err == nil {
		t.Error("proof must keep a VM with a new connection")
	}
	e.run.On([]string{"lsof"}, "", &execx.ExitError{Code: 1})
	e.run.On([]string{"podman", "ps", "-a"}, psJSON("running", 0, 1759999990, `[]`), nil)
	if err := (&cat{}).Proof(e.Env, vm); err == nil {
		t.Error("proof must keep a VM with a running container")
	}
}

func psJSON(state string, exitedAt, startedAt int64, ports string) string {
	return fmt.Sprintf(`[{"Id":%q,"State":%q,"ExitedAt":%d,"StartedAt":%d,"Created":1700000000,"Mounts":[],"Ports":%s}]`,
		idOld, state, exitedAt, startedAt, ports)
}

// idleCtrs is a list of only old, exited containers.
var idleCtrs = psJSON("exited", 1756112000, 1700000100, `[]`)

func TestNeverStartsEngine(t *testing.T) {
	e := podmanEnv(t)
	e.run.On([]string{"podman", "system", "df"}, "", errors.New("unable to connect to podman machine"))
	e.run.On([]string{"podman", "machine", "list"}, strings.Replace(fixture(t, "podman-machine-list.json"), `"Running": true`, `"Running": false`, 1), nil)
	cands, _ := scan(t, e)
	for _, c := range cands {
		if c.Removal == core.ToolCommand {
			t.Errorf("candidate with a stopped machine: %v", c.Command)
		}
	}
	for _, call := range e.run.Calls {
		for _, w := range []string{"start", "init", "up", "run", "create"} {
			if slices.Contains(call.Argv, w) {
				t.Errorf("scan ran %v", call.Argv)
			}
		}
	}
}

func TestRootfulDockerReportOnly(t *testing.T) {
	e := podmanEnv(t)
	delete(e.run.Paths, "podman")
	e.run.Paths["docker"] = "/usr/bin/docker"
	e.plat.OSName = "linux"
	e.plat.NotOwned = map[string]bool{"/var/lib/docker": true}
	e.run.On([]string{"docker", "system", "df"}, `{"Type":"Images","Size":"1GB","Reclaimable":"1GB"}`+"\n", nil)
	e.run.On([]string{"docker", "images"}, "sha256:aaa\n", nil)
	e.run.On([]string{"docker", "ps"}, fixture(t, "podman-ps-a.json"), nil)
	cands, _ := scan(t, e)
	if len(cands) == 0 {
		t.Error("rootful docker is not even reported")
	}
	for _, c := range cands {
		if c.Removal != core.ReportOnly {
			t.Errorf("rootful docker candidate is removable: %+v", c)
		}
	}
}
