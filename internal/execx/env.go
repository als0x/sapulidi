package execx

import (
	"path/filepath"
	"slices"
	"strings"
)

// scrubbedEnv are the git variables a git hook sets. A run started from a hook must not let them redirect the
// git commands the guards run, so Real removes them from every child's environment.
var scrubbedEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR", "GIT_CEILING_DIRECTORIES",
}

// childEnv is the environment of one child: scrubbed, and for git also without the system git config
// (GIT_CONFIG_NOSYSTEM=1). The user's global config stays: its ignore rules matter. The runner sets it so no
// argv builder has to change the process environment.
func childEnv(argv0 string, env []string) []string {
	out := scrubEnv(env)
	if filepath.Base(argv0) != "git" {
		return out
	}
	out = slices.DeleteFunc(out, func(kv string) bool { return strings.HasPrefix(kv, "GIT_CONFIG_NOSYSTEM=") })
	return append(out, "GIT_CONFIG_NOSYSTEM=1")
}

// scrubEnv returns env without the scrubbed variables.
func scrubEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(scrubbedEnv, name) {
			out = append(out, kv)
		}
	}
	return out
}
