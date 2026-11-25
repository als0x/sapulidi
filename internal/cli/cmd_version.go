package cli

import (
	"encoding/json"
	"fmt"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/install"
	"github.com/als0x/sapulidi/internal/version"
)

func init() {
	Register(Command{Name: "version", Summary: "print the version", Run: runVersion})
}

func runVersion(app *App, _ []string) int {
	if app.JSON {
		b, _ := json.Marshal(map[string]string{"version": version.Version, "repo": version.Repo})
		fmt.Fprintln(app.Stdout, string(b))
		return 0
	}
	fmt.Fprintln(app.Stdout, version.Version)
	if version.Repo == "" {
		return 0
	}
	if paths, err := config.ResolvePaths(); err == nil {
		if cfg, _, err := config.Load(paths.Config); err == nil && updateCheckEnabled(cfg) {
			if n := updateNotice(paths.State, install.Marketplace); n != "" {
				fmt.Fprintln(app.Stdout, n)
			}
		}
	}
	return 0
}
