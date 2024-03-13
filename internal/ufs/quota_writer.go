// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright (c) 2024 Matthew Penner

package ufs

import (
	"errors"
	"io"
	"sync/atomic"
)

// CountedWriter is a writer that counts the amount of data written to the
// underlying writer.
type CountedWriter struct {
	File

	counter atomic.Int64
	err     error
}

// NewCountedWriter returns a new countedWriter that counts the amount of bytes
// written to the underlying writer.
func NewCountedWriter(f File) *CountedWriter {
	return &CountedWriter{File: f}
}

// BytesWritten returns the amount of bytes that have been written to the
// underlying writer.
func (w *CountedWriter) BytesWritten() int64 {
	return w.counter.Load()
}

// Error returns the error from the writer if any. If the error is an EOF, nil
// will be returned.
func (w *CountedWriter) Error() error {
	if errors.Is(w.err, io.EOF) {
		return nil
	}
	return w.err
}

// Write writes bytes to the underlying writer while tracking the total amount
// of bytes written.
func (w *CountedWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, io.EOF
	}

	// Write is a very simple operation for us to handle.
	n, err := w.File.Write(p)
	w.counter.Add(int64(n))
	w.err = err

	// TODO: is this how we actually want to handle errors with this?
	if err == io.EOF {
		return n, io.EOF
	} else {
		return n, nil
	}
}

func (w *CountedWriter) ReadFrom(r io.Reader) (n int64, err error) {
	cr := NewCountedReader(r)
	n, err = w.File.ReadFrom(cr)
	w.counter.Add(n)
	return
}

// CountedReader is a reader that counts the amount of data read from the
// underlying reader.
type CountedReader struct {
	reader io.Reader

	counter atomic.Int64
	err     error
}

var _ io.Reader = (*CountedReader)(nil)

// NewCountedReader returns a new countedReader that counts the amount of bytes
// read from the underlying reader.
func NewCountedReader(r io.Reader) *CountedReader {
	return &CountedReader{reader: r}
}

// BytesRead returns the amount of bytes that have been read from the underlying
// reader.
func (r *CountedReader) BytesRead() int64 {
	return r.counter.Load()
}

// Error returns the error from the reader if any. If the error is an EOF, nil
// will be returned.
func (r *CountedReader) Error() error {
	if errors.Is(r.err, io.EOF) {
		return nil
	}
	return r.err
}

// Read reads bytes from the underlying reader while tracking the total amount
// of bytes read.
func (r *CountedReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, io.EOF
	}

	n, err := r.reader.Read(p)
	r.counter.Add(int64(n))
	r.err = err

	if err == io.EOF {
		return n, io.EOF
	} else {
		return n, nil
	}
}
