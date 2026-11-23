package main

import (
	"bytes"
	"testing"
)

func TestRefusesRoot(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"version"}, &out, &errb, func() int { return 0 })
	if code != 1 || errb.String() != "sapulidi does not run as root\n" || out.Len() != 0 {
		t.Fatalf("code %d stderr %q", code, errb.String())
	}
	code = run([]string{"version"}, &out, &errb, func() int { return 501 })
	if code != 0 {
		t.Fatalf("non-root code %d", code)
	}
}
