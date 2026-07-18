package download

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// gzipMagic is the two-byte gzip header.
var gzipMagic = []byte{0x1f, 0x8b}

// DefaultMaxBinaryBytes caps the decompressed mihomo binary (~128 MiB; real
// builds are ~50 MiB stripped).
const DefaultMaxBinaryBytes int64 = 128 << 20

// GunzipFile decompresses a gzip asset at srcPath into a single regular file at
// dstPath. It verifies the gzip magic, decompresses only the first member
// (rejecting concatenated archives), enforces a size cap, and chmods the result
// to 0755.
func GunzipFile(srcPath, dstPath string, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBinaryBytes
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer src.Close()

	// Magic check before handing to gzip.Reader.
	head := make([]byte, 2)
	if _, err := io.ReadFull(src, head); err != nil {
		return fmt.Errorf("read gzip header: %w", err)
	}
	if head[0] != gzipMagic[0] || head[1] != gzipMagic[1] {
		return fmt.Errorf("not a gzip archive (bad magic %x)", head)
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind gzip: %w", err)
	}

	gz, err := gzip.NewReader(src)
	if err != nil {
		return fmt.Errorf("open gzip reader: %w", err)
	}
	defer gz.Close()
	gz.Multistream(false) // first member only; reject concatenated archives

	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create binary: %w", err)
	}
	defer dst.Close()

	n, err := io.Copy(dst, io.LimitReader(gz, maxBytes+1))
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	if n > maxBytes {
		return fmt.Errorf("decompressed size exceeds limit %d", maxBytes)
	}
	if err := dst.Chmod(0o755); err != nil {
		return fmt.Errorf("chmod binary: %w", err)
	}
	if err := dst.Sync(); err != nil {
		return fmt.Errorf("fsync binary: %w", err)
	}
	return nil
}

// SHA256File returns the hex SHA-256 digest of a file, streamed.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// IsExecutableRegularFile reports whether path is a regular file the current
// user can execute. Used for offline --from validation.
func IsExecutableRegularFile(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return false, nil
	}
	return fi.Mode().Perm()&0o100 != 0, nil
}
