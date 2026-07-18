// Package atomicfile provides crash-safe file writes via write-temp + fsync +
// atomic rename. All persistent MiHatch state (config, source snapshot, runtime
// config, state.json, proxy-snapshot.json, engine metadata) is written through
// this package so a crash can never leave a half-written critical file.
package atomicfile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// maxRandomSuffix is the byte length of the random suffix appended to temp
// names to avoid collisions between concurrent writers.
const randomSuffixLen = 4

// WriteFile writes data to filename atomically with the given mode. The temp
// file is created in the same directory as the destination (so rename is
// guaranteed to be atomic on the same filesystem) and fsync'd before rename.
// The destination directory must already exist.
func WriteFile(filename string, mode os.FileMode, data []byte) error {
	return WriteFileVia(filename, mode, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// WriteFileVia atomically writes filename by streaming into a caller-supplied
// function. Use this for large or generated content (downloads, YAML encode)
// to avoid buffering the whole payload in memory.
func WriteFileVia(filename string, mode os.FileMode, write func(io.Writer) error) (retErr error) {
	dir := filepath.Dir(filename)
	if dir == "" {
		return errors.New("atomicfile: empty directory")
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("atomicfile: target dir: %w", err)
	}

	tmp, err := tempName(dir, filepath.Base(filename))
	if err != nil {
		return fmt.Errorf("atomicfile: temp name: %w", err)
	}

	// Create the temp file mode-restricted; we chmod to the exact target mode
	// after closing so umask cannot relax it.
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("atomicfile: create temp: %w", err)
	}
	defer func() {
		if retErr != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()

	if err := write(f); err != nil {
		return fmt.Errorf("atomicfile: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("atomicfile: fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("atomicfile: close temp: %w", err)
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return fmt.Errorf("atomicfile: chmod: %w", err)
	}
	if err := os.Rename(tmp, filename); err != nil {
		return fmt.Errorf("atomicfile: rename: %w", err)
	}
	return syncDir(dir)
}

// syncDir fsyncs the directory holding filename so the rename is durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		// Some filesystems don't support directory fsync; ignore where it is
		// unavailable rather than failing a successful content write.
		return nil
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		if errors.Is(err, fs.ErrInvalid) || errors.Is(err, os.ErrInvalid) {
			return nil
		}
		return fmt.Errorf("atomicfile: sync dir: %w", err)
	}
	return nil
}

// tempName builds a sibling temp path: .<base>.<rand>.tmp
func tempName(dir, base string) (string, error) {
	buf := make([]byte, randomSuffixLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	name := fmt.Sprintf(".%s.%s.tmp", base, hex.EncodeToString(buf))
	return filepath.Join(dir, name), nil
}
