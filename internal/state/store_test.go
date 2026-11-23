package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/als0x/sapulidi/internal/config"
	"github.com/als0x/sapulidi/internal/testutil"
)

// Paused fails closed: when the pause file cannot be looked at, the run must not go on.
func TestPausedFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	home := testutil.TempHome(t)
	p := config.Paths{
		Config: filepath.Join(home, ".config", "sapulidi", "config.toml"),
		State:  filepath.Join(home, ".local", "state", "sapulidi"),
		Data:   filepath.Join(home, ".local", "share", "sapulidi"),
	}
	st, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Paused() {
		t.Fatal("paused without a pause file")
	}
	if err := os.Chmod(p.State, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p.State, 0o700) })
	if !st.Paused() {
		t.Fatal("an unreadable state directory must count as paused")
	}
	if err := os.Chmod(p.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if st.Paused() {
		t.Fatal("still paused after the directory is readable again")
	}
	st.Pause()
	if !st.Paused() {
		t.Fatal("pause file not seen")
	}
}
