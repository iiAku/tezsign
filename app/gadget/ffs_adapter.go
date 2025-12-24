package main

import (
	"context"
	"errors"
	"os"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// Instrumentation: track leaked goroutines from context cancellation
var (
	leakedReaders atomic.Int64
	leakedWriters atomic.Int64
)

// GetLeakStats returns the current count of leaked reader and writer goroutines
func GetLeakStats() (readers, writers int64) {
	return leakedReaders.Load(), leakedWriters.Load()
}

type result struct {
	n   int
	err error
}

type Reader struct {
	fd int
}

type Writer struct {
	fd int
}

// we know that this is potentially leaking goroutines
// but as there are no available context-aware read/write for os.File
// this is the simplest way to achieve it for now

func NewReader(f *os.File) (*Reader, error) {
	return &Reader{fd: int(f.Fd())}, nil
}
func NewWriter(f *os.File) (*Writer, error) {
	return &Writer{fd: int(f.Fd())}, nil
}

func (r *Reader) ReadContext(ctx context.Context, p []byte) (int, error) {
	readChan := make(chan result, 1)

	go func() {
		n, err := unix.Read(r.fd, p)
		readChan <- result{n: n, err: err}
	}()

	select {
	case <-ctx.Done():
		leakedReaders.Add(1) // Instrumentation: goroutine is now leaked
		return 0, ctx.Err()
	case res := <-readChan:
		if errors.Is(res.err, os.ErrDeadlineExceeded) {
			return 0, ctx.Err()
		}
		return res.n, res.err
	}
}

func (w *Writer) WriteContext(ctx context.Context, p []byte) (int, error) {
	writeChan := make(chan result, 1)

	go func() {
		written := 0
		total := len(p)
		for written < total {
			n, err := unix.Write(w.fd, p[written:])
			if err != nil {
				writeChan <- result{n: written, err: err}
				return
			}
			written += n
		}
		writeChan <- result{n: written, err: nil}
	}()

	select {
	case <-ctx.Done():
		leakedWriters.Add(1) // Instrumentation: goroutine is now leaked
		return 0, ctx.Err()
	case res := <-writeChan:
		if errors.Is(res.err, os.ErrDeadlineExceeded) {
			return 0, ctx.Err()
		}
		return res.n, res.err
	}
}
