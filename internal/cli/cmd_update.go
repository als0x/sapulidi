package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/confirm"
	"github.com/als0x/sapulidi/internal/install"
	"github.com/als0x/sapulidi/internal/state"
	"github.com/als0x/sapulidi/internal/update"
	"github.com/als0x/sapulidi/internal/version"
)

// updateAPIBase is the release API, updateClient the client for it and the downloads, and updateDefaultBases says
// where a release is downloaded from; tests replace them.
var (
	updateAPIBase      = update.APIBase
	updateClient       = &http.Client{}
	updateDefaultBases = update.DefaultBases
)

// updateTimeout bounds the request to the release API.
const updateTimeout = 3 * time.Second

const updateUsage = "usage: sapulidi update [--check-only] [--quiet] [--yes]"

func init() {
	Register(Command{Name: "update", Summary: "install the latest release (--check-only only asks)", Run: runUpdate})
}

// runUpdate: with --quiet anywhere in the arguments nothing is printed, a usage error included, and the exit code
// is 0.
func runUpdate(app *App, args []string) int {
	for _, a := range args {
		switch a {
		case "--quiet", "-quiet", "--quiet=true", "-quiet=true":
			silent := *app
			silent.Stdout, silent.Stderr = io.Discard, io.Discard
			updateCommand(&silent, args)
			return 0
		}
	}
	return updateCommand(app, args)
}

func updateCommand(app *App, args []string) int {
	fs := newFlags(app, "update")
	checkOnly := fs.Bool("check-only", false, "only ask for the latest release and remember it")
	yes := fs.Bool("yes", false, "update without asking")
	fs.Bool("quiet", false, "print nothing and exit 0; errors go to the log") // handled by runUpdate
	if code, ok := parseFlags(app, fs, args); !ok {
		return code
	}
	if fs.NArg() > 0 {
		return usageFail(app, "%s", updateUsage)
	}
	if !*checkOnly {
		return selfUpdate(app, *yes)
	}
	tag, off, err := checkLatest()
	switch {
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

// selfUpdate updates an install.sh install. A plugin install is told how to update the plugin instead.
func selfUpdate(app *App, yes bool) int {
	if version.Repo == "" {
		return fail(app, "update", errors.New("this build names no release repository"))
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return fail(app, "update", err)
	}
	routes := update.Routes(paths.State)
	if len(routes) == 0 {
		return fail(app, "update", fmt.Errorf("how this copy was installed is not recorded; "+
			"get the new release from https://github.com/%s/releases", version.Repo))
	}
	own := slices.Contains(routes, update.RouteInstallSh)
	if own && !yes && !confirm.StdioIsTerminal() {
		return fail(app, "update", errors.New("run this in your own terminal, or add --yes"))
	}
	tag, off, err := checkLatest()
	if err == nil && off != "" {
		err = errors.New("nothing updated: " + off)
	}
	if err != nil {
		return fail(app, "update", err)
	}
	var from update.Bases // set when something is to be downloaded
	report := func(res update.Installed, notice, text string) int {
		if app.JSON {
			writeJSON(app.Stdout, map[string]any{"updated": res.Moved, "running": version.Version, "latest": tag,
				"current": res.Current, "notice": notice, "release_base": from.Release, "raw_base": from.Raw})
		} else {
			fmt.Fprintln(app.Stdout, text)
		}
		return 0
	}
	if !update.Newer(tag, version.Version) {
		return report(update.Installed{}, "", fmt.Sprintf("latest release %s, you have %s: nothing to do",
			safeText(tag), version.Version))
	}
	if !own {
		notice := update.Notice(version.Version, update.Cache{Tag: tag}, routes, install.Marketplace)
		return report(update.Installed{}, notice, notice)
	}
	change := version.Version + " → " + safeText(tag)
	from = updateDefaultBases(version.Repo, tag).FromEnv()
	if !app.JSON {
		fmt.Fprintln(app.Stdout, change)
		fmt.Fprintf(app.Stdout, "download from %s (checksums from %s)\n", safeText(from.Release), safeText(from.Raw))
	}
	if !yes {
		err := confirm.Terminal("Update Sapu Lidi "+change+"? [y/N] ", "y")
		if errors.Is(err, confirm.ErrMismatch) {
			err = errors.New("nothing changed")
		}
		if err != nil {
			return fail(app, "update", err)
		}
	}
	res, err := update.Install(context.Background(), updateClient, paths.Data, tag, from)
	if err != nil {
		logUpdate(paths, "update-failed", map[string]any{"tag": tag, "error": err.Error()})
		return fail(app, "update", err)
	}
	logUpdate(paths, "update", map[string]any{"tag": tag, "current": res.Current})
	text := "updated to " + tag
	if res.Current != res.Version {
		text = fmt.Sprintf("installed %s; %s is newer and stays current", tag, res.Current)
	}
	return report(res, "", text)
}

// logUpdate writes one line to the log, as far as the state directory allows.
func logUpdate(paths config.Paths, kind string, fields map[string]any) {
	if st, err := state.Open(paths); err == nil {
		st.Logger().Event(kind, fields)
	}
}

// autoUpdate is the opt-in update after a scheduled run: update.auto, an install.sh install, once a day, never with
// a person at a terminal. It downloads from the default bases only, whatever the environment says. It prints
// nothing; a failure is logged and tried again the next day. check says once that an update happened.
func autoUpdate() {
	if version.Repo == "" || confirm.StdioIsTerminal() {
		return
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		return
	}
	cfg, _, err := config.Load(paths.Config)
	if err != nil || len(cfg.Invalid) > 0 || !cfg.Update.Check || !cfg.Update.Auto ||
		!slices.Contains(update.Routes(paths.State), update.RouteInstallSh) {
		return
	}
	st, err := state.Open(paths)
	if err != nil {
		return
	}
	failed := func(err error) {
		st.Logger().Event("update-auto-failed", map[string]any{"error": err.Error()})
	}
	// The day's slot of its own, apart from the one of the check that `check` starts.
	slot := filepath.Join(paths.State, "auto-update")
	if err := os.MkdirAll(slot, 0o700); err != nil {
		failed(err)
		return
	}
	if won, err := update.Claim(slot); !won {
		if err != nil {
			failed(err)
		}
		return
	}
	tag, off, err := checkLatest()
	if err != nil {
		failed(err)
		return
	}
	if off != "" || !update.Newer(tag, version.Version) {
		return
	}
	res, err := update.Install(context.Background(), updateClient, paths.Data, tag, updateDefaultBases(version.Repo, tag))
	if err != nil {
		failed(err)
		return
	}
	if !res.Moved {
		return
	}
	st.Logger().Event("update-auto", map[string]any{"tag": tag})
	st.Fact("update-auto", "Sapu Lidi updated itself to "+tag+".", true)
	st.MarkUndelivered("update-auto") // check shows it once
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
	tag, err = update.Fetch(ctx, updateClient, updateAPIBase, version.Repo)
	if err != nil {
		return "", "", err
	}
	return tag, "", update.SaveCache(paths.State, update.Cache{Tag: tag, CheckedAt: time.Now()})
}
