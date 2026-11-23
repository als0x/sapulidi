package guard

import (
	"context"
	"errors"
	"os"
	"testing"
)

type failRunner struct{}

func (failRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("boom")
}
func (failRunner) LookPath(string) (string, error) { return "", errors.New("no") }

func removeAll(t *testing.T, p string) {
	t.Helper()
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
}
