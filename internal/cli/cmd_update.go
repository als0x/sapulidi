package cli

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/internal/update"
	"github.com/als0x/sapulidi/internal/version"
)

// updateAPIBase is the release API; tests replace it.
var updateAPIBase = update.APIBase

// updateTimeout bounds the whole request.
const updateTimeout = 3 * time.Second

const updateUsage = "usage: sapulidi update --check-only [--quiet]"

func init() {
	Register(Command{Name: "update", Summary: "ask whether a newer release exists (--check-only)", Run: runUpdate})
}

func runUpdate(app *App, args []string) int {
	fs := newFlags(app, "update")
	checkOnly := fs.Bool("check-only", false, "only ask for the latest release and remember it")
	quiet := fs.Bool("quiet", false, "print nothing and exit 0; errors go to the log")
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}
	if !*checkOnly {
		return usageFail(app, "%s", updateUsage)
	}
	tag, off, err := checkLatest()
	switch {
	case *quiet:
	case err != nil:
		return fail(app, "update", err)
	case app.JSON:
		writeJSON(app.Stdout, map[string]any{"checked": off == "", "current": version.Version, "latest": tag,
			"newer": update.Newer(tag, version.Version)})
	case off != "":
		fmt.Fprintln(app.Stdout, "sapulidi: nothing checked: "+off)
	default:
		fmt.Fprintf(app.Stdout, "latest release %s, you have %s\n", safeText(tag), version.Version)
	}
	return 0
}

// checkLatest fetches the latest tag and caches it. off says why no request was made; a failed request or write is
// also logged, which is the only trace the detached child leaves.
func checkLatest() (tag, off string, err error) {
	if version.Repo == "" {
		return "", "this build names no release repository", nil
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return "", "", err
	}
	st, err := state.Open(paths)
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			st.Logger().Event("update-check-failed", map[string]any{"error": err.Error()})
		}
	}()
	cfg, _, err := config.Load(paths.Config)
	switch {
	case err != nil:
		return "", "", err
	case len(cfg.Invalid) > 0: // it may be the file that switches the check off
		return "", invalidConfigMessage(cfg.Invalid[0]), nil
	case !cfg.Update.Check:
		return "", "update.check is false", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()
	tag, err = update.Fetch(ctx, &http.Client{}, updateAPIBase, version.Repo)
	if err != nil {
		return "", "", err
	}
	return tag, "", update.SaveCache(paths.State, update.Cache{Tag: tag, CheckedAt: time.Now()})
}
