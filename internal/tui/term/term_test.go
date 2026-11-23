package term

import (
	"os"
	"testing"
)

func TestPlain(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	cases := []struct {
		name, noColor, term string
		want                bool
	}{
		{"colour terminal", "", "xterm-256color", false},
		{"NO_COLOR set", "1", "xterm-256color", true},
		{"TERM dumb", "", "dumb", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", c.noColor)
			t.Setenv("TERM", c.term)
			if got := newTerm(null, null).Plain; got != c.want {
				t.Fatalf("Plain = %v, want %v", got, c.want)
			}
		})
	}
}
