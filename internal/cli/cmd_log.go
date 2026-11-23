package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/als0x/sapulidi/internal/config"
)

const (
	defaultLogLimit = 20
	maxLogLine      = 1 << 20 // a longer line is skipped
)

func init() {
	Register(Command{Name: "log", Summary: "show the last log events (--limit N)", Run: runLog})
}

func runLog(app *App, args []string) int {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", defaultLogLimit, "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *limit < 1 {
		return usageFail(app, "usage: sapulidi log [--limit N]   (N at least 1)")
	}
	p, err := config.ResolvePaths()
	if err != nil {
		return fail(app, "log", err)
	}
	lines, err := lastLines(filepath.Join(p.State, "log.jsonl"), *limit)
	if err != nil {
		return fail(app, "log", err)
	}
	if app.JSON {
		events := make([]json.RawMessage, 0, len(lines))
		for _, l := range lines {
			events = append(events, json.RawMessage(l))
		}
		writeJSON(app.Stdout, map[string]any{"events": events})
		return 0
	}
	if len(lines) == 0 {
		fmt.Fprintln(app.Stdout, "the log is empty")
	}
	for _, l := range lines {
		fmt.Fprintln(app.Stdout, safeText(l))
	}
	return 0
}

// lastLines returns the last n lines of path that are valid JSON; a missing file is an empty log. The log is
// rotated at 1 MB, so reading it whole is fine.
func lastLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var all []string
	r := bufio.NewReaderSize(f, 64*1024)
	var buf []byte
	oversize := false
	for {
		chunk, more, err := r.ReadLine()
		if err != nil { // io.EOF ends the file; any other error ends the read with what was found
			break
		}
		if !oversize {
			buf = append(buf, chunk...)
			if len(buf) > maxLogLine {
				oversize, buf = true, nil // a line this long is skipped, not an error
			}
		}
		if more {
			continue
		}
		if !oversize && json.Valid(buf) {
			all = append(all, string(buf))
		}
		buf, oversize = buf[:0], false
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}
