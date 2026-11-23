package tui

import (
	"fmt"
	"strings"
	"time"
)

// fmtBytes prints a size with one decimal in 1024 units; a negative size is unknown.
func fmtBytes(n int64) string {
	if n < 0 {
		return "?"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// fmtAge prints the time from t to now: days, months or years; "-" when t is unknown.
func fmtAge(t, now time.Time) string {
	if t.IsZero() || now.IsZero() {
		return "-"
	}
	days := int(now.Sub(t).Hours() / 24)
	switch {
	case days < 1:
		return "<1d"
	case days < 60:
		return fmt.Sprintf("%dd", days)
	case days < 730:
		return fmt.Sprintf("%dmo", days/30)
	}
	return fmt.Sprintf("%dy", days/365)
}

func fmtDate(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02")
}

func fmtInterval(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}

// fit cuts or pads s to exactly w runes.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) > w {
		return string(r[:w])
	}
	return s + strings.Repeat(" ", w-len(r))
}

// fitRight is fit with the text right-aligned.
func fitRight(s string, w int) string {
	r := []rune(s)
	if len(r) >= w {
		return fit(s, w)
	}
	return strings.Repeat(" ", w-len(r)) + s
}

// shorten cuts the middle of s so that it is at most w runes.
func shorten(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 3 {
		return string(r[:max(w, 0)])
	}
	keep := w - 3
	head := (keep + 1) / 2
	tail := keep - head
	return string(r[:head]) + "..." + string(r[len(r)-tail:])
}

// wrap breaks s into chunks of at most w runes.
func wrap(s string, w int) []string {
	r := []rune(s)
	if w <= 0 || len(r) <= w {
		return []string{s}
	}
	var out []string
	for len(r) > w {
		out = append(out, string(r[:w]))
		r = r[w:]
	}
	return append(out, string(r))
}

// style wraps a whole line in an SGR code unless the model is plain.
func style(plain bool, code, s string) string {
	if plain || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func repeat(s string, n int) string {
	if n < 0 {
		n = 0
	}
	return strings.Repeat(s, n)
}
