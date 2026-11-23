package execx

import (
	"context"
	"strings"
	"testing"
)

// The system git config is switched off by the runner, for a git child only: a git alias that runs `env` shows
// the child's environment.
func TestRealRunnerSwitchesOffSystemGitConfigForGit(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "")
	out, err := Real{}.Run(context.Background(), "", "git", "-c", "alias.penv=!env", "penv")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains("\n"+string(out), "\nGIT_CONFIG_NOSYSTEM=1\n") {
		t.Fatalf("a git child did not get GIT_CONFIG_NOSYSTEM=1:\n%s", out)
	}
	out, err = Real{}.Run(context.Background(), "", "env")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "GIT_CONFIG_NOSYSTEM=1") {
		t.Fatal("a child that is not git got GIT_CONFIG_NOSYSTEM=1")
	}
}

func TestChildEnvGitOnlyAndNoDuplicate(t *testing.T) {
	env := []string{"A=1", "GIT_CONFIG_NOSYSTEM=0", "GIT_DIR=/x"}
	got := childEnv("/usr/bin/git", env)
	n := 0
	for _, kv := range got {
		if kv == "GIT_CONFIG_NOSYSTEM=1" {
			n++
		}
		if kv == "GIT_CONFIG_NOSYSTEM=0" || kv == "GIT_DIR=/x" {
			t.Fatalf("kept %s", kv)
		}
	}
	if n != 1 {
		t.Fatalf("env %v", got)
	}
	for _, kv := range childEnv("ls", env) {
		if kv == "GIT_CONFIG_NOSYSTEM=1" {
			t.Fatal("non-git child got the variable")
		}
	}
}

func TestRealRunnerScrubsGitEnv(t *testing.T) {
	for _, k := range scrubbedEnv {
		t.Setenv(k, "/nonexistent/"+k)
	}
	t.Setenv("SAPULIDI_KEEP_ME", "yes")
	out, err := Real{}.Run(context.Background(), "", "env")
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, _, ok := strings.Cut(line, "="); ok {
			have[k] = true
		}
	}
	for _, k := range scrubbedEnv {
		if have[k] {
			t.Errorf("%s reached the child", k)
		}
	}
	if !have["SAPULIDI_KEEP_ME"] || !have["PATH"] {
		t.Errorf("other variables were dropped: %v", have)
	}
}

func TestScrubEnvKeepsLookalikes(t *testing.T) {
	in := []string{"GIT_DIR=/x", "GIT_DIRECTORY=/y", "GIT_WORK_TREE=/z", "A=B"}
	got := scrubEnv(in)
	if len(got) != 2 || got[0] != "GIT_DIRECTORY=/y" || got[1] != "A=B" {
		t.Fatalf("got %v", got)
	}
}
