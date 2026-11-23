package procs

import (
	"path/filepath"
	"strings"
)

// matchesPattern says whether a command line is a dev server or helper an agent starts. It anchors on the
// executable (argv[0], or the script after node) and the specific sub-command, never on a word somewhere in
// the line.
func matchesPattern(cmd string) bool {
	argv := strings.Fields(cmd)
	if len(argv) == 0 {
		return false
	}
	arg := func(i int) string {
		if i < len(argv) {
			return argv[i]
		}
		return ""
	}
	switch exe := exeBase(cmd); {
	case exe == "node" || exe == "nodejs":
		return nodeScript(argv[1:])
	case isPython(exe):
		return arg(1) == "-m" && arg(2) == "http.server"
	case exe == "npm" || exe == "pnpm" || exe == "bun":
		return arg(1) == "run" && oneOf(arg(2), "dev", "start", "serve")
	case exe == "uvicorn", exe == "air":
		return true
	case exe == "flask":
		return arg(1) == "run"
	case exe == "go":
		return arg(1) == "run"
	case exe == "kubectl":
		return arg(1) == "port-forward"
	case exe == "playwright":
		return arg(1) == "test" && hasArg(argv[1:], "--ui")
	}
	return headlessBrowser(cmd)
}

// nodeScript: args follow "node"; the first non-flag is the script.
func nodeScript(args []string) bool {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		i++
	}
	if i == len(args) {
		return false
	}
	name := strings.ToLower(filepath.Base(args[i]))
	for _, ext := range []string{".js", ".mjs", ".cjs"} {
		name = strings.TrimSuffix(name, ext)
	}
	rest := args[i+1:]
	sub := ""
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") { // the sub-command is the word right after the script
		sub = rest[0]
	}
	switch name {
	case "vite":
		return oneOf(sub, "", "dev", "serve", "preview")
	case "next":
		return oneOf(sub, "dev", "start")
	case "nuxt":
		return oneOf(sub, "dev", "preview")
	case "nuxi":
		return sub == "dev"
	case "webpack":
		return oneOf(sub, "serve", "watch")
	case "webpack-dev-server":
		return true
	case "storybook":
		return sub == "dev"
	case "playwright":
		return sub == "test" && hasArg(rest, "--ui")
	}
	return false
}

// headlessBrowser: a headless Chrome or Chromium with a remote-debugging port. The executable may contain
// spaces (macOS app bundles), so it is everything before the first flag.
func headlessBrowser(cmd string) bool {
	if !strings.Contains(cmd, "--remote-debugging-port") || !strings.Contains(cmd, "--headless") {
		return false
	}
	exe := cmd
	if i := strings.Index(cmd, " --"); i >= 0 {
		exe = cmd[:i]
	}
	return strings.Contains(strings.ToLower(filepath.Base(exe)), "chrom")
}

func isPython(exe string) bool {
	rest, ok := strings.CutPrefix(exe, "python")
	if !ok {
		return false
	}
	return strings.Trim(rest, "0123456789.") == ""
}

func oneOf(s string, options ...string) bool {
	for _, o := range options {
		if s == o {
			return true
		}
	}
	return false
}

func hasArg(args []string, want string) bool { return oneOf(want, args...) }

// listenerExcluded are programs that listen on a port by design; a listener is never an orphan because of one.
var listenerExcluded = []string{
	"postgres", "pg_ctl", "redis-server", "mysqld", "mongod", "docker", "podman", "tmux", "screen", "ssh",
	"ssh-agent", "sshd", "gopls", "rust-analyzer", "tsserver", "code", "cursor", "claude", "codex", "herdr",
}

func excludedListener(cmd string) bool {
	exe := exeBase(cmd)
	return strings.HasPrefix(exe, "qemu-system") || oneOf(exe, listenerExcluded...)
}
