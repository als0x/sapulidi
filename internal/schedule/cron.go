package schedule

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/core"
)

// Cron keeps one marked block per label in the user crontab; every other line stays byte for byte.
type Cron struct {
	Home    string // temp files for `crontab <file>` go here
	PathEnv string
	Run     core.Runner
	Reason  string // why cron was picked over systemd; shown by Status
}

func (b *Cron) Name() string { return "cron" }

func beginMark(label string) string { return "# sapulidi:" + label + " begin\n" }
func endMark(label string) string   { return "# sapulidi:" + label + " end\n" }

// cronSpec turns an interval into a cron expression. Cron has no "every N" beyond a field's range, so only these
// intervals exist: whole minutes 5 to 59 (*/N), whole hours up to 23 (0 */N), exactly 1d (daily at 03:17) and
// exactly 7d (weekly, Sunday 03:17). Anything else is refused, never rounded.
func cronSpec(interval time.Duration) (string, error) {
	switch {
	case interval == 7*24*time.Hour:
		return "17 3 * * 0", nil
	case interval == 24*time.Hour:
		return "17 3 * * *", nil
	case interval >= 5*time.Minute && interval < time.Hour && interval%time.Minute == 0:
		return fmt.Sprintf("*/%d * * * *", int(interval/time.Minute)), nil
	case interval >= time.Hour && interval < 24*time.Hour && interval%time.Hour == 0:
		return fmt.Sprintf("0 */%d * * *", int(interval/time.Hour)), nil
	}
	return "", fmt.Errorf("cron cannot run every %s; use 5 to 59 minutes, whole hours up to 23, 1d or 7d",
		config.FormatInterval(interval))
}

func (b *Cron) block(j Job, pathEnv string) (string, error) {
	// % is a newline in a crontab line and escaping it is shell-dependent: refuse instead.
	vals := append([]string{j.Label, j.LogPath, pathEnv}, j.Argv...)
	for _, v := range j.Env {
		vals = append(vals, v.Value)
	}
	for _, v := range vals {
		if strings.Contains(v, "%") {
			return "", fmt.Errorf("schedule: cron cannot carry %q: '%%' is not allowed", v)
		}
	}
	spec, err := cronSpec(j.Interval)
	if err != nil {
		return "", err
	}
	words := make([]string, len(j.Argv))
	for i, a := range j.Argv {
		words[i] = quoteSh(a)
	}
	env := b.EnvFragment(EnvVar{"PATH", pathEnv})
	for _, v := range j.Env {
		env += b.EnvFragment(v)
	}
	line := spec + " " + env + strings.Join(words, " ") + " >/dev/null 2>>" + quoteSh(j.LogPath)
	return beginMark(j.Label) + line + "\n" + endMark(j.Label), nil
}

// EnvFragment is one NAME='value' prefix word of the cron line, followed by a space.
func (b *Cron) EnvFragment(v EnvVar) string { return v.Name + "=" + quoteSh(v.Value) + " " }

func (b *Cron) InstalledText(label string) (string, bool) {
	cur, err := b.list()
	if err != nil {
		return "", false
	}
	s, e, ok, err := span(cur, label)
	if err != nil || !ok {
		return "", false
	}
	return cur[s:e], true
}

// list reads the user crontab. "No crontab" is empty; any other failure is an error, so a failed
// read never leads to writing over the user's crontab.
func (b *Cron) list() (string, error) {
	out, err := b.Run.Run(ctx(), "", "crontab", "-l")
	if err != nil {
		if ee, ok := exitError(err); ok && strings.Contains(ee.Stderr, "no crontab") {
			return "", nil
		}
		return "", fmt.Errorf("crontab -l: %w", err)
	}
	return string(out), nil
}

func (b *Cron) write(content string) error {
	if err := needHome(b.Home); err != nil {
		return err
	}
	f, err := os.CreateTemp(b.Home, ".sapulidi-crontab-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if _, err := b.Run.Run(ctx(), "", "crontab", name); err != nil {
		return fmt.Errorf("crontab: %w", err)
	}
	return nil
}

// lineIndex finds mark at the start of a line (offset 0 or right after a newline) at or after from.
func lineIndex(content, mark string, from int) int {
	for from <= len(content) {
		i := strings.Index(content[from:], mark)
		if i < 0 {
			return -1
		}
		i += from
		if i == 0 || content[i-1] == '\n' {
			return i
		}
		from = i + 1
	}
	return -1
}

// span finds the block of label: start of the begin line, end after the end line. Markers match only
// at the start of a line. A begin without an end is an error: the block cannot be replaced safely.
func span(content, label string) (int, int, bool, error) {
	s := lineIndex(content, beginMark(label), 0)
	if s < 0 {
		return 0, 0, false, nil
	}
	e := lineIndex(content, endMark(label), s+len(beginMark(label)))
	if e < 0 {
		return 0, 0, false, fmt.Errorf("schedule: crontab has %q without %q; fix it by hand", strings.TrimSpace(beginMark(label)), strings.TrimSpace(endMark(label)))
	}
	return s, e + len(endMark(label)), true, nil
}

func (b *Cron) Install(j Job) error {
	pathEnv := pathOrDefault(b.PathEnv)
	if err := checkJob(j, pathEnv); err != nil {
		return err
	}
	blk, err := b.block(j, pathEnv)
	if err != nil {
		return err
	}
	cur, err := b.list()
	if err != nil {
		return err
	}
	s, e, ok, err := span(cur, j.Label)
	if err != nil {
		return err
	}
	if ok {
		cur = cur[:s] + blk + cur[e:]
	} else {
		if cur != "" && !strings.HasSuffix(cur, "\n") {
			cur += "\n"
		}
		cur += blk
	}
	return b.write(cur)
}

func (b *Cron) Remove(label string) error {
	cur, err := b.list()
	if err != nil {
		return err
	}
	s, e, ok, err := span(cur, label)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return b.write(cur[:s] + cur[e:])
}

func (b *Cron) Status(label string) (Status, error) {
	cur, err := b.list()
	if err != nil {
		return Status{}, err
	}
	_, _, ok, err := span(cur, label)
	if err != nil {
		return Status{}, err
	}
	st := Status{Installed: ok}
	if b.Reason != "" {
		st.Warnings = append(st.Warnings, b.Reason)
	}
	return st, nil
}
