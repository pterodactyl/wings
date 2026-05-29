package progress

import (
	"io"
	"strings"
	"sync/atomic"

	"github.com/pterodactyl/wings/system"
)

// Progress is used to track the progress of any I/O operation that are being
// performed.
type Progress struct {
	// written is the total size of the files that have been written to the writer.
	written uint64
	// Total is the total size of the archive in bytes.
	total uint64

	// Writer .
	Writer io.Writer
}

// NewProgress returns a new progress tracker for the given total size.
func NewProgress(total uint64) *Progress {
	return &Progress{total: total}
}

// Written returns the total number of bytes written.
// This function should be used when the progress is tracking data being written.
func (p *Progress) Written() uint64 {
	return atomic.LoadUint64(&p.written)
}

// Total returns the total size in bytes.
func (p *Progress) Total() uint64 {
	return atomic.LoadUint64(&p.total)
}

// SetTotal sets the total size of the archive in bytes. This function is safe
// to call concurrently and can be used to update the total size if it changes,
// such as when the total size is simultaneously being calculated as data is
// being written through the progress writer.
func (p *Progress) SetTotal(total uint64) {
	atomic.StoreUint64(&p.total, total)
}

// Write totals the number of bytes that have been written to the writer.
func (p *Progress) Write(v []byte) (int, error) {
	n := len(v)
	atomic.AddUint64(&p.written, uint64(n))
	if p.Writer != nil {
		return p.Writer.Write(v)
	}
	return n, nil
}

// Progress returns a formatted progress string for the current progress.
func (p *Progress) Progress(width int) string {
	// current = 100 (Progress, dynamic)
	// total = 1000 (Content-Length, dynamic)
	// width = 25 (Number of ticks to display, static)
	// widthPercentage = 100 / width (What percentage does each tick represent, static)
	//
	// percentageDecimal = current / total = 0.1
	// percentage = percentageDecimal * 100 = 10%
	// ticks = percentage / widthPercentage = 2.5
	//
	// ticks is a float64, so we cast it to an int which rounds it down to 2.

	// Values are cast to floats to prevent integer division.
	current := p.Written()
	total := p.Total()
	// width := is passed as a parameter
	widthPercentage := float64(100) / float64(width)
	percentageDecimal := float64(current) / float64(total)
	percentage := percentageDecimal * 100
	ticks := int(percentage / widthPercentage)

	// Ensure that we never get a negative number of ticks, this will prevent strings#Repeat
	// from panicking.  A negative number of ticks is likely to happen when the total size is
	// inaccurate, such as when we are going off of rough disk usage calculation.
	if ticks < 0 {
		ticks = 0
	} else if ticks > width {
		ticks = width
	}

	bar := strings.Repeat("=", ticks) + strings.Repeat(" ", width-ticks)
	return "[" + bar + "] " + system.FormatBytes(current) + " / " + system.FormatBytes(total)
}
