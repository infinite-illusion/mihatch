package lock

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcquireReleaseSerializes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "test.lock")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	l1, err := Acquire(ctx, path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer l1.Release()

	// A second acquisition on the same file must block until released.
	var (
		wg      sync.WaitGroup
		gotLock atomic.Bool
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		quick, qcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer qcancel()
		l2, err := Acquire(quick, path)
		if err != nil {
			return
		}
		gotLock.Store(true)
		_ = l2.Release()
	}()

	// Give the goroutine a moment to confirm it is blocked.
	time.Sleep(100 * time.Millisecond)
	if gotLock.Load() {
		t.Fatalf("second holder acquired while first still held the lock")
	}

	_ = l1.Release()
	wg.Wait()
	if !gotLock.Load() {
		t.Fatalf("second holder never acquired after release")
	}
}

func TestAcquireCancelled(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cancel.lock")
	l1, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer l1.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err = Acquire(ctx, path)
	if err == nil {
		t.Fatalf("expected cancellation error")
	}
}

func TestReleaseIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "id.lock")
	l, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release should be no-op, got %v", err)
	}
}
