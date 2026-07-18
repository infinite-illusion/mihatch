package atomicfile

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileCreatesAndPersists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	if err := WriteFile(target, 0o600, []byte("hello")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("got %q want hello", b)
	}
	fi, _ := os.Stat(target)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o want 0600", fi.Mode().Perm())
	}
}

func TestWriteFileNoTempLeftOnError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	wantErr := errors.New("boom")
	err := WriteFileVia(target, 0o600, func(w io.Writer) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped boom, got %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file %q left behind after failed write", e.Name())
		}
	}
	// Target must not exist either.
	if _, err := os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("target should not exist after failed write: %v", err)
	}
}

func TestWriteFileOverwritesAtomically(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	if err := WriteFile(target, 0o600, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(target, 0o600, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "v2" {
		t.Fatalf("got %q want v2", b)
	}
}

func TestWriteFileMissingDir(t *testing.T) {
	t.Parallel()
	if err := WriteFile("/no/such/dir/x", 0o600, []byte("x")); err == nil {
		t.Fatal("expected error for missing dir")
	}
}
