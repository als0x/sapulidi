package report

import (
	"strings"
	"unicode"
)

const maxText = 200

// Clean makes untrusted text (a path, a name, a git or guard detail) safe to print and to hand to a model: control
// characters, line and paragraph separators and bidi override and isolate characters become '?', and the text is
// cut to 200 runes with an ellipsis.
func Clean(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == maxText {
			b.WriteString("…")
			break
		}
		switch {
		case unicode.IsControl(r), r == 0x2028, r == 0x2029, r == 0x061C, r == 0x200E, r == 0x200F,
			r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == unicode.ReplacementChar:
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
		n++
	}
	return b.String()
}
