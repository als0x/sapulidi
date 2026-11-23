package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
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

// sizeText prints a known total, and when some sizes are unknown says "at least" plus how many.
func sizeText(known int64, unknown int) string {
	if unknown == 0 {
		return fmtBytes(known)
	}
	return fmt.Sprintf("at least %s, plus %d of unknown size", fmtBytes(known), unknown)
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

// clean replaces every control character (tab, newline, ESC, DEL, C1) and invalid byte with '?'.
func clean(s string) string {
	ok := true
	for _, r := range s {
		if isControl(r) || r == utf8.RuneError {
			ok = false
			break
		}
	}
	if ok {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if isControl(r) || r == utf8.RuneError {
			r = '?'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isControl(r rune) bool { return r < 0x20 || (r >= 0x7f && r < 0xa0) || r == 0x2028 || r == 0x2029 }

// runeWidth is the number of terminal columns of r: 0 for combining marks, 2 for wide CJK and emoji, else 1.
func runeWidth(r rune) int {
	switch {
	case r >= 0x300 && r <= 0x36f, r >= 0x200b && r <= 0x200f, r >= 0xfe00 && r <= 0xfe0f:
		return 0
	case r >= 0x1100 && r <= 0x115f,
		r >= 0x2e80 && r <= 0xa4cf,
		r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff,
		r >= 0xfe30 && r <= 0xfe6f,
		r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1f64f,
		r >= 0x1f900 && r <= 0x1f9ff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

// cols is the width of s in terminal columns.
func cols(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

// cut sanitises s and keeps the longest prefix of at most w columns.
func cut(s string, w int) string {
	s = clean(s)
	if w <= 0 {
		return ""
	}
	n := 0
	for i, r := range s {
		n += runeWidth(r)
		if n > w {
			return s[:i]
		}
	}
	return s
}

// fit cuts or pads s to exactly w columns.
func fit(s string, w int) string {
	s = cut(s, w)
	return s + strings.Repeat(" ", max(w-cols(s), 0))
}

// fitRight is fit with the text right-aligned.
func fitRight(s string, w int) string {
	s = cut(s, w)
	return strings.Repeat(" ", max(w-cols(s), 0)) + s
}

// shorten cuts the middle of s so that it is at most w columns.
func shorten(s string, w int) string {
	s = clean(s)
	if cols(s) <= w {
		return s
	}
	if w <= 3 {
		return cut(s, w)
	}
	keep := w - 3
	head := cut(s, (keep+1)/2)
	tail := keep - cols(head)
	r := []rune(s)
	start := len(r)
	for n := 0; start > 0 && n+runeWidth(r[start-1]) <= tail; start-- {
		n += runeWidth(r[start-1])
	}
	return head + "..." + string(r[start:])
}

// wrap breaks s into chunks of at most w columns.
func wrap(s string, w int) []string {
	s = clean(s)
	if w <= 0 || cols(s) <= w {
		return []string{s}
	}
	var out []string
	for s != "" {
		c := cut(s, w)
		if c == "" {
			_, n := utf8.DecodeRuneInString(s)
			c = s[:n]
		}
		out = append(out, c)
		s = s[len(c):]
	}
	return out
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
