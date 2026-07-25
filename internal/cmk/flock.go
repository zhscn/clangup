package cmk

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// cmk takes two kinds of file lock, both through here: one per build
// directory (two concurrent configures must not run cmake into the same
// cache) and one per dependency store entry (two worktrees syncing at
// once build it exactly once).

// lockFile takes an exclusive flock on path, creating the file and its
// parent. When blocking is false, ok reports whether the lock was free:
// a held lock is not an error, so `cmk clean --prune` can simply skip an
// entry a concurrent sync is building.
func lockFile(path string, blocking bool) (lock *os.File, ok bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	how := syscall.LOCK_EX
	if !blocking {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), how); err != nil {
		file.Close()
		if !blocking {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("locking %s: %w", path, err)
	}
	return file, true, nil
}

func unlockFile(lock *os.File) {
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	lock.Close()
}

// lockBuildDir serializes configures of one build dir. The lock lives in
// the build tree itself, which this creates if it is the first configure.
func lockBuildDir(dir string) (*os.File, error) {
	lock, _, err := lockFile(filepath.Join(dir, ".cmk-lock"), true)
	return lock, err
}

// storeEntryLock is the lock path for one store entry. It lives outside
// the entry so wiping a half-built entry can't drop the lock.
func storeEntryLock(name, stamp string) string {
	if len(stamp) > 16 {
		stamp = stamp[:16]
	}
	return filepath.Join(storeDir(), ".locks", name+"-"+stamp+".lock")
}

func lockStoreEntry(name, stamp string) (*os.File, error) {
	lock, _, err := lockFile(storeEntryLock(name, stamp), true)
	return lock, err
}

// tryLockStoreEntry is the non-blocking form: ok is false (with no error)
// when another process holds the lock.
func tryLockStoreEntry(name, stamp string) (lock *os.File, ok bool, err error) {
	return lockFile(storeEntryLock(name, stamp), false)
}
