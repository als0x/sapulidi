package term

import (
	"reflect"
	"testing"
)

func r(c rune) Key        { return Key{Rune: c} }
func sp(s SpecialKey) Key { return Key{Special: s} }

func TestDecode(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		final bool
		want  []Key
		n     int
	}{
		{"printable", "abc", false, []Key{r('a'), r('b'), r('c')}, 3},
		{"space", " ", false, []Key{r(' ')}, 1},
		{"enter cr", "\r", false, []Key{sp(KeyEnter)}, 1},
		{"enter lf", "\n", false, []Key{sp(KeyEnter)}, 1},
		{"tab", "\t", false, []Key{sp(KeyTab)}, 1},
		{"backspace 7f", "\x7f", false, []Key{sp(KeyBackspace)}, 1},
		{"backspace 08", "\x08", false, []Key{sp(KeyBackspace)}, 1},
		{"ctrl-c", "\x03", false, []Key{{Rune: 'c', Ctrl: true}}, 1},
		{"ctrl-u", "\x15", false, []Key{{Rune: 'u', Ctrl: true}}, 1},
		{"utf8", "é世", false, []Key{r('é'), r('世')}, 5},
		{"partial utf8 waits", "\xc3", false, nil, 0},
		{"partial utf8 dropped at final", "\xc3", true, nil, 1},
		{"lone esc waits", "\x1b", false, nil, 0},
		{"lone esc at final", "\x1b", true, []Key{sp(KeyEsc)}, 1},
		{"esc esc", "\x1b\x1b", false, []Key{sp(KeyEsc)}, 1},
		{"arrow up", "\x1b[A", false, []Key{sp(KeyUp)}, 3},
		{"arrow down", "\x1b[B", false, []Key{sp(KeyDown)}, 3},
		{"arrow right", "\x1b[C", false, []Key{sp(KeyRight)}, 3},
		{"arrow left", "\x1b[D", false, []Key{sp(KeyLeft)}, 3},
		{"ss3 up", "\x1bOA", false, []Key{sp(KeyUp)}, 3},
		{"ss3 left", "\x1bOD", false, []Key{sp(KeyLeft)}, 3},
		{"home csi", "\x1b[H", false, []Key{sp(KeyHome)}, 3},
		{"end csi", "\x1b[F", false, []Key{sp(KeyEnd)}, 3},
		{"home tilde", "\x1b[1~", false, []Key{sp(KeyHome)}, 4},
		{"end tilde", "\x1b[4~", false, []Key{sp(KeyEnd)}, 4},
		{"home ss3", "\x1bOH", false, []Key{sp(KeyHome)}, 3},
		{"pgup", "\x1b[5~", false, []Key{sp(KeyPgUp)}, 4},
		{"pgdn", "\x1b[6~", false, []Key{sp(KeyPgDn)}, 4},
		{"delete", "\x1b[3~", false, []Key{sp(KeyDelete)}, 4},
		{"partial csi waits", "\x1b[", false, nil, 0},
		{"partial csi dropped at final", "\x1b[", true, nil, 2},
		{"unknown csi dropped whole", "\x1b[Zx", false, []Key{r('x')}, 4},
		{"unknown tilde dropped whole", "\x1b[99~x", false, []Key{r('x')}, 6},
		{"unknown ss3 dropped whole", "\x1bOZx", false, []Key{r('x')}, 4},
		{"alt key dropped", "\x1bxy", false, []Key{r('y')}, 3},
		{"garbage bytes dropped", "\xff\xfea", false, []Key{r('a')}, 3},
		{"csi then text", "\x1b[Aq", false, []Key{sp(KeyUp), r('q')}, 4},
		{"incomplete tail kept", "a\x1b[", false, []Key{r('a')}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, n := Decode([]byte(c.in), c.final)
			if !reflect.DeepEqual(got, c.want) || n != c.n {
				t.Fatalf("Decode(%q, %v) = %v, %d; want %v, %d", c.in, c.final, got, n, c.want, c.n)
			}
		})
	}
}

// feed emulates the reader loop: chunks arrive one by one, undecoded bytes wait for more.
func feed(chunks ...string) []Key {
	var out []Key
	var pending []byte
	for _, c := range chunks {
		pending = append(pending, c...)
		keys, n := Decode(pending, false)
		out = append(out, keys...)
		pending = pending[n:]
	}
	keys, _ := Decode(pending, true)
	return append(out, keys...)
}

func TestDecodeSplitReads(t *testing.T) {
	const in = "a\x1b[Ab\xc3\xa9\x1b[3~\x1bOB\x1b"
	want := feed(in)
	if len(want) != 7 {
		t.Fatalf("whole read gave %v", want)
	}
	for i := 1; i < len(in); i++ {
		if got := feed(in[:i], in[i:]); !reflect.DeepEqual(got, want) {
			t.Fatalf("split at %d: %v, want %v", i, got, want)
		}
	}
	var bytewise []string
	for i := 0; i < len(in); i++ {
		bytewise = append(bytewise, in[i:i+1])
	}
	if got := feed(bytewise...); !reflect.DeepEqual(got, want) {
		t.Fatalf("byte by byte: %v, want %v", got, want)
	}
}

func TestDecodeOverlongCSIDropped(t *testing.T) {
	in := "\x1b[11111111111111111111111111111111111111111111111111"
	keys, n := Decode([]byte(in), false)
	if n == 0 || len(keys) != 0 {
		t.Fatalf("overlong sequence must be dropped, not wait forever: keys=%v n=%d", keys, n)
	}
}
