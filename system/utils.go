package system

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
)

var (
	cr  = []byte(" \r")
	crr = []byte("\r\n")
)

// FirstNotEmpty returns the first string passed in that is not an empty value.
func FirstNotEmpty(v ...string) string {
	for _, val := range v {
		if val != "" {
			return val
		}
	}
	return ""
}

func MustInt(v string) int {
	i, err := strconv.Atoi(v)
	if err != nil {
		panic(errors.Wrap(err, "system/utils: could not parse int"))
	}
	return i
}

func ScanReader(r io.Reader, callback func(line string)) error {
	br := bufio.NewReader(r)
	// Avoid constantly re-allocating memory when we're flooding lines through this
	// function by using the same buffer for the duration of the call and just truncating
	// the value back to 0 every loop.
	var str strings.Builder
	for {
		str.Reset()
		var err error
		var line []byte
		var isPrefix bool

		for {
			// Read the line and write it to the buffer.
			line, isPrefix, err = br.ReadLine()
			// Certain games like Minecraft output absolutely random carriage returns in the output seemingly
			// in line with that it thinks is the terminal size. Those returns break a lot of output handling,
			// so we'll just replace them with proper new-lines and then split it later and send each line as
			// its own event in the response.
			str.Write(bytes.Replace(line, cr, crr, -1))
			// Finish this loop and begin outputting the line if there is no prefix (the line fit into
			// the default buffer), or if we hit the end of the line.
			if !isPrefix || err == io.EOF {
				break
			}
			// If we encountered an error with something in ReadLine that was not an EOF just abort
			// the entire process here.
			if err != nil {
				return err
			}
		}
		// Publish the line for this loop. Break on new-line characters so every line is sent as a single
		// output event, otherwise you get funky handling in the browser console.
		for _, line := range strings.Split(str.String(), "\r\n") {
			callback(line)
		}
		// If the error we got previously that lead to the line being output is an io.EOF we want to
		// exit the entire looping process.
		if err == io.EOF {
			break
		}
	}
	return nil
}

// Runs a given work function every "d" duration until the provided context is canceled.
func Every(ctx context.Context, d time.Duration, work func(t time.Time)) {
	ticker := time.NewTicker(d)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case t := <-ticker.C:
				work(t)
			}
		}
	}()
}

func FormatBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(1024), 0
	for n := b / 1024; n >= 1024; n /= 1024 {
		div *= 1024
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

type AtomicBool struct {
	v  bool
	mu sync.RWMutex
}

func NewAtomicBool(v bool) *AtomicBool {
	return &AtomicBool{v: v}
}

func (ab *AtomicBool) Store(v bool) {
	ab.mu.Lock()
	ab.v = v
	ab.mu.Unlock()
}

// Stores the value "v" if the current value stored in the AtomicBool is the opposite
// boolean value. If successfully swapped, the response is "true", otherwise "false"
// is returned.
func (ab *AtomicBool) SwapIf(v bool) bool {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	if ab.v != v {
		ab.v = v
		return true
	}
	return false
}

func (ab *AtomicBool) Load() bool {
	ab.mu.RLock()
	defer ab.mu.RUnlock()
	return ab.v
}

func (ab *AtomicBool) UnmarshalJSON(b []byte) error {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	return json.Unmarshal(b, &ab.v)
}

func (ab *AtomicBool) MarshalJSON() ([]byte, error) {
	return json.Marshal(ab.Load())
}

// AtomicString allows for reading/writing to a given struct field without having to worry
// about a potential race condition scenario. Under the hood it uses a simple sync.RWMutex
// to control access to the value.
type AtomicString struct {
	v  string
	mu sync.RWMutex
}

func NewAtomicString(v string) *AtomicString {
	return &AtomicString{v: v}
}

// Stores the string value passed atomically.
func (as *AtomicString) Store(v string) {
	as.mu.Lock()
	as.v = v
	as.mu.Unlock()
}

// Loads the string value and returns it.
func (as *AtomicString) Load() string {
	as.mu.RLock()
	defer as.mu.RUnlock()
	return as.v
}

func (as *AtomicString) UnmarshalJSON(b []byte) error {
	as.mu.Lock()
	defer as.mu.Unlock()
	return json.Unmarshal(b, &as.v)
}

func (as *AtomicString) MarshalJSON() ([]byte, error) {
	return json.Marshal(as.Load())
}
