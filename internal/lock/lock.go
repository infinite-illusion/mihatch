// Package lock provides a context-aware exclusive file lock backed by BSD
// flock(2). It is the single serialization point for MiHatch commands so that
// two concurrent "mihatch up" / "mihatch down" processes cannot race on the
// system proxy or launchd job.
package lock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// Lock represents a held exclusive lock on a file.
type Lock struct {
	f *os.File
}

// pollInterval is the delay between non-blocking flock attempts while waiting.
const pollInterval = 50 * time.Millisecond

// Acquire blocks until it obtains an exclusive lock on path or ctx is
// cancelled. The lock file is created if absent.
func Acquire(ctx context.Context, path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		// Non-blocking exclusive lock.
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return &Lock{f: f}, nil
		}
		if ctx.Err() != nil {
			_ = f.Close()
			return nil, ctx.Err()
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Release drops the lock and closes the underlying file. It is safe to call
// multiple times.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	l.f = nil
	return err
}

// IsBusy reports whether err represents contention on the lock.
func IsBusy(err error) bool { return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) }
