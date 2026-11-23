package term

import "unicode/utf8"

// SpecialKey names a key that has no printable rune.
type SpecialKey int

const (
	KeyNone SpecialKey = iota
	KeyEnter
	KeyEsc
	KeyBackspace
	KeyTab
	KeyUp
	KeyDown
	KeyRight
	KeyLeft
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDn
	KeyDelete
)

// Event is a Key or a Resize.
type Event interface{ isEvent() }

// Key is one decoded key press. A printable key has Rune set; Ctrl-letter has Ctrl set and the
// lower-case letter in Rune; the rest set Special.
type Key struct {
	Rune    rune
	Special SpecialKey
	Ctrl    bool
}

// Resize reports the new terminal size.
type Resize struct{ Cols, Rows int }

func (Key) isEvent()    {}
func (Resize) isEvent() {}

const maxCSI = 32

// Decode turns the bytes of b into keys and returns how many bytes it consumed. An incomplete tail
// (a lone ESC, a partial escape sequence, a partial UTF-8 rune) is left unconsumed so the caller can
// append the next read; with final set (the ESC timeout passed, no more bytes came) a lone ESC
// becomes the Esc key and any other incomplete tail is dropped. Unknown sequences are dropped whole.
func Decode(b []byte, final bool) (keys []Key, n int) {
	for n < len(b) {
		c := b[n]
		switch {
		case c == 0x1b:
			k, used, ok := decodeEsc(b[n:], final)
			if !ok {
				return keys, n
			}
			if k.Special != KeyNone {
				keys = append(keys, k)
			}
			n += used
		case c == '\r' || c == '\n':
			keys = append(keys, Key{Special: KeyEnter})
			n++
		case c == '\t':
			keys = append(keys, Key{Special: KeyTab})
			n++
		case c == 0x7f || c == 0x08:
			keys = append(keys, Key{Special: KeyBackspace})
			n++
		case c >= 1 && c <= 26:
			keys = append(keys, Key{Rune: rune('a' + c - 1), Ctrl: true})
			n++
		case c < 0x20:
			n++ // other control bytes are dropped
		case c < utf8.RuneSelf:
			keys = append(keys, Key{Rune: rune(c)})
			n++
		default:
			if !utf8.FullRune(b[n:]) {
				if !final {
					return keys, n
				}
				n = len(b)
				continue
			}
			ru, size := utf8.DecodeRune(b[n:])
			if ru != utf8.RuneError {
				keys = append(keys, Key{Rune: ru})
			}
			n += size
		}
	}
	return keys, n
}

// decodeEsc decodes one sequence that starts with ESC. ok is false when more bytes are needed.
// A returned key with Special KeyNone means the sequence was consumed and dropped.
func decodeEsc(b []byte, final bool) (k Key, used int, ok bool) {
	if len(b) == 1 {
		if final {
			return Key{Special: KeyEsc}, 1, true
		}
		return Key{}, 0, false
	}
	switch b[1] {
	case 0x1b:
		return Key{Special: KeyEsc}, 1, true
	case 'O':
		if len(b) < 3 {
			return incomplete(b, final)
		}
		return Key{Special: finalKey(b[2])}, 3, true
	case '[':
		for i := 2; i < len(b) && i < maxCSI; i++ {
			c := b[i]
			switch {
			case c >= 0x30 && c <= 0x3f, c >= 0x20 && c <= 0x2f:
				continue
			case c >= 0x40 && c <= 0x7e:
				return Key{Special: csiKey(b[2:i], c)}, i + 1, true
			}
			return Key{}, i, true // not a valid sequence byte: drop what came before it
		}
		if len(b) >= maxCSI {
			return Key{}, len(b), true
		}
		return incomplete(b, final)
	}
	return Key{}, 2, true // ESC plus another byte (alt-key): dropped
}

func incomplete(b []byte, final bool) (Key, int, bool) {
	if final {
		return Key{}, len(b), true
	}
	return Key{}, 0, false
}

func finalKey(c byte) SpecialKey {
	switch c {
	case 'A':
		return KeyUp
	case 'B':
		return KeyDown
	case 'C':
		return KeyRight
	case 'D':
		return KeyLeft
	case 'H':
		return KeyHome
	case 'F':
		return KeyEnd
	}
	return KeyNone
}

func csiKey(params []byte, final byte) SpecialKey {
	if final != '~' {
		return finalKey(final)
	}
	switch string(params) {
	case "1", "7":
		return KeyHome
	case "4", "8":
		return KeyEnd
	case "3":
		return KeyDelete
	case "5":
		return KeyPgUp
	case "6":
		return KeyPgDn
	}
	return KeyNone
}
