package state

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/als0x/sapulidi/internal/execx"
)

// ErrBusy means another sapulidi run holds the lock.
var ErrBusy = errors.New("state: another sapulidi run holds the lock")

// startSkew is how far two readings of one process's start time may differ (Linux derives it from boot time).
const startSkew = 2 * time.Second

// ownerlessMaxAge is how long a lock directory without a readable owner blocks before it is taken over.
const ownerlessMaxAge = 60 * time.Minute

func randHex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Lock takes the run lock (spec S11): the directory <state>/lock holding pid, start (the owner's process start
// time) and token. A live owner with the same start time blocks with ErrBusy. A dead or recycled owner, or an
// owner-less lock older than 60 minutes, is taken over by renaming it away first, so two racers cannot both win.
// The returned release removes the lock only when it is still ours.
func (s *Store) Lock() (func(), error) {
	pid := os.Getpid()
	start, err := execx.ProcessStart(pid)
	if err != nil {
		return nil, err
	}
	token := randHex()
	lock := s.path("lock")
	s.sweepLockLeftovers()

	for attempt := 0; attempt < 4; attempt++ {
		if _, err := os.Lstat(lock); errors.Is(err, fs.ErrNotExist) {
			// Build the complete lock next to it, then rename: the lock never exists half-written.
			tmp, err := os.MkdirTemp(s.dir, ".lock-tmp-")
			if err != nil {
				return nil, err
			}
			ok := writeOwner(tmp, pid, start, token) == nil
			if ok && os.Rename(tmp, lock) == nil {
				return s.releaser(lock, token), nil
			}
			_ = os.RemoveAll(tmp)
			// a racer won the rename (or the write failed); look again
			if !ok {
				return nil, errors.New("state: cannot write the lock")
			}
			continue
		}
		judged, _ := readOwnerFile(lock, "token")
		if !s.lockStale(lock) {
			return nil, ErrBusy
		}
		if s.afterStaleCheck != nil {
			s.afterStaleCheck()
		}
		// Take over: the rename is the commit point, only one racer can move this directory.
		trash := lock + ".stale-" + randHex()
		if err := os.Rename(lock, trash); err != nil {
			continue
		}
		if s.afterRename != nil {
			s.afterRename()
		}
		// What we moved must be what we judged. A faster racer may have replaced the stale lock with a live
		// one between our check and the rename: put it back, and if that fails keep it where it is.
		if got, _ := readOwnerFile(trash, "token"); got != judged {
			if err := os.Rename(trash, lock); err != nil {
				return nil, ErrBusy // the trash directory stays; nothing is deleted
			}
			return nil, ErrBusy
		}
		_ = os.RemoveAll(trash)
	}
	return nil, ErrBusy
}

func writeOwner(dir string, pid int, start time.Time, token string) error {
	for name, v := range map[string]string{
		"pid":   strconv.Itoa(pid),
		"start": strconv.FormatInt(start.UnixNano(), 10),
		"token": token,
	} {
		if err := os.WriteFile(dir+"/"+name, []byte(v+"\n"), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func readOwnerFile(dir, name string) (string, bool) {
	b, err := os.ReadFile(dir + "/" + name)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// lockStale reports whether the lock directory may be taken over. It fails closed: when it cannot tell, the
// lock is held.
func (s *Store) lockStale(dir string) bool {
	pidS, ok1 := readOwnerFile(dir, "pid")
	startS, ok2 := readOwnerFile(dir, "start")
	pid, err1 := strconv.Atoi(pidS)
	startNs, err2 := strconv.ParseInt(startS, 10, 64)
	if !ok1 || !ok2 || err1 != nil || err2 != nil || pid <= 0 {
		fi, err := os.Lstat(dir)
		return err == nil && s.now().Sub(fi.ModTime()) > ownerlessMaxAge
	}
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true // dead owner
	}
	cur, err := execx.ProcessStart(pid)
	if err != nil {
		return false // cannot read the start time of a process that exists: keep the lock
	}
	diff := cur.Sub(time.Unix(0, startNs))
	if diff < 0 {
		diff = -diff
	}
	return diff > startSkew // a clearly different start time means the pid was recycled
}

func (s *Store) releaser(lock, token string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			if t, ok := readOwnerFile(lock, "token"); !ok || t != token {
				return // taken over by someone else; not ours to remove
			}
			trash := lock + ".release-" + randHex()
			if os.Rename(lock, trash) == nil {
				_ = os.RemoveAll(trash)
			}
		})
	}
}

// sweepLockLeftovers removes temp and stale lock directories a crashed run left behind, once they are old.
func (s *Store) sweepLockLeftovers() {
	for _, pat := range []string{".lock-tmp-*", "lock.stale-*", "lock.release-*"} {
		matches, _ := filepath.Glob(filepath.Join(s.dir, pat))
		for _, m := range matches {
			if fi, err := os.Lstat(m); err == nil && fi.IsDir() && s.now().Sub(fi.ModTime()) > ownerlessMaxAge {
				_ = os.RemoveAll(m)
			}
		}
	}
}
