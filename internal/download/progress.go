package download

import (
	"fmt"
	"io"
	"time"
)

// progressWriter wraps an io.Writer to report periodic download progress as a
// carriage-return-updated line, throttled so it never floods the terminal. It
// never returns an error so it cannot fail the download.
//
// It supports resume: setRange(base, total) sets how many bytes are already on
// disk (base) before the current attempt, so the bar continues across retries.
type progressWriter struct {
	w       io.Writer
	total   int64
	base    int64
	written int64
	last    time.Time
	started bool
}

func newProgressWriter(w io.Writer, total int64) *progressWriter {
	return &progressWriter{w: w, total: total}
}

// setRange configures the bar for a (possibly resumed) attempt.
func (p *progressWriter) setRange(base, total int64) {
	p.base = base
	p.total = total
	p.written = 0
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n := len(b)
	p.written += int64(n)
	now := time.Now()
	if !p.started {
		p.render()
		p.started = true
		p.last = now
		return n, nil
	}
	if now.Sub(p.last) >= 100*time.Millisecond || (p.total > 0 && p.base+p.written >= p.total) {
		p.render()
		p.last = now
	}
	return n, nil
}

func (p *progressWriter) render() {
	cur := p.base + p.written
	if p.total > 0 {
		pct := cur * 100 / p.total
		fmt.Fprintf(p.w, "\r  %s / %s  (%d%%)   ", humanBytes(cur), humanBytes(p.total), pct)
	} else {
		fmt.Fprintf(p.w, "\r  %s downloaded   ", humanBytes(cur))
	}
}

// note ends the current progress line and prints a fresh line (used for retry
// notices). The next Write re-arms the bar.
func (p *progressWriter) note(format string, args ...any) {
	if p.started {
		fmt.Fprint(p.w, "\n")
		p.started = false
	}
	fmt.Fprintf(p.w, "  "+format+"\n", args...)
}

// finish ends the progress line so later output starts fresh.
func (p *progressWriter) finish() {
	if p.started {
		fmt.Fprint(p.w, "\n")
		p.started = false
	}
}

// humanBytes renders a byte count with one decimal and a binary unit suffix.
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

var _ io.Writer = (*progressWriter)(nil)
