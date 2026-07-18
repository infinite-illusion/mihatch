package download

import (
	"context"
	"io"
	"sync/atomic"
	"time"
)

// stallReader wraps a response body and cancels the request context if no bytes
// are read for the configured duration. This replaces a fixed whole-request
// timeout: on a flaky link a transfer can legitimately take minutes, so we only
// give up when the connection has truly stalled (no progress), then the caller
// resumes from disk.
type stallReader struct {
	rc      io.ReadCloser
	timeout time.Duration
	last    atomic.Int64 // unix-nano of last successful read
	cancel  context.CancelFunc
	done    chan struct{}
}

func newStallReader(rc io.ReadCloser, timeout time.Duration, cancel context.CancelFunc) *stallReader {
	s := &stallReader{
		rc:      rc,
		timeout: timeout,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	s.last.Store(time.Now().UnixNano())
	go s.watch()
	return s
}

func (s *stallReader) watch() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			last := time.Unix(0, s.last.Load())
			if time.Since(last) > s.timeout {
				s.cancel()
				return
			}
		}
	}
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.rc.Read(p)
	if n > 0 {
		s.last.Store(time.Now().UnixNano())
	}
	return n, err
}

func (s *stallReader) Close() error {
	select {
	case <-s.done:
		// already closed
	default:
		close(s.done)
	}
	return s.rc.Close()
}
