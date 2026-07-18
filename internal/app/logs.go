package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"mihatch/internal/exit"
)

// Logs prints the tail of mihomo.log. With follow, it streams appended lines
// until the context is cancelled (Ctrl-C).
func (a *App) Logs(ctx context.Context, follow bool, tailLines int) error {
	path := a.Paths.LogFile()
	f, err := os.Open(path)
	if err != nil {
		return exit.New(exit.CodeUninitialized, fmt.Errorf("no mihomo log yet (is MiHatch initialized/up?): %w", err))
	}
	defer f.Close()

	if err := a.writeTail(f, tailLines); err != nil {
		return exit.New(exit.CodeGeneral, err)
	}
	if !follow {
		return nil
	}

	// Follow: poll for appended bytes until cancelled.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return exit.New(exit.CodeGeneral, err)
	}
	reader := bufio.NewReader(f)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	flusher, _ := a.Out.(interface{ Sync() error })
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				fmt.Fprint(a.Out, line)
				if flusher != nil {
					_ = flusher.Sync()
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return exit.New(exit.CodeGeneral, err)
			}
		}
	}
}

// writeTail prints the last tailLines lines of f (or all if tailLines<=0).
func (a *App) writeTail(f *os.File, tailLines int) error {
	if tailLines <= 0 {
		_, err := io.Copy(a.Out, f)
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	all, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	lines := splitLines(string(all))
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	for _, l := range lines {
		fmt.Fprintln(a.Out, l)
	}
	return nil
}

// splitLines splits on newlines, dropping a trailing empty element produced by
// a final newline.
func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 || i == start {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
