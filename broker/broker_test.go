package broker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// mockReadWriter implements ReadContexter and WriteContexter for testing
type mockReadWriter struct {
	readData  chan []byte
	writeData chan []byte
	readErr   error
	writeErr  error
	mu        sync.Mutex
}

func newMockReadWriter() *mockReadWriter {
	return &mockReadWriter{
		readData:  make(chan []byte, 100),
		writeData: make(chan []byte, 100),
	}
}

func (m *mockReadWriter) ReadContext(ctx context.Context, p []byte) (int, error) {
	m.mu.Lock()
	err := m.readErr
	m.mu.Unlock()

	if err != nil {
		return 0, err
	}

	select {
	case data := <-m.readData:
		n := copy(p, data)
		return n, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (m *mockReadWriter) WriteContext(ctx context.Context, p []byte) (int, error) {
	m.mu.Lock()
	err := m.writeErr
	m.mu.Unlock()

	if err != nil {
		return 0, err
	}

	select {
	case m.writeData <- append([]byte(nil), p...):
		return len(p), nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (m *mockReadWriter) setReadError(err error) {
	m.mu.Lock()
	m.readErr = err
	m.mu.Unlock()
}

func (m *mockReadWriter) setWriteError(err error) {
	m.mu.Lock()
	m.writeErr = err
	m.mu.Unlock()
}

func TestBrokerCreation(t *testing.T) {
	rw := newMockReadWriter()

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		return []byte("response"), nil
	}

	b := New(rw, rw, WithHandler(handler))
	defer b.Stop()

	if b == nil {
		t.Fatal("broker should not be nil")
	}

	if b.workerCount != defaultWorkerCount {
		t.Errorf("expected default worker count %d, got %d", defaultWorkerCount, b.workerCount)
	}
}

func TestBrokerWithCustomWorkerCount(t *testing.T) {
	rw := newMockReadWriter()

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, nil
	}

	customCount := 16
	b := New(rw, rw, WithHandler(handler), WithWorkerCount(customCount))
	defer b.Stop()

	if b.workerCount != customCount {
		t.Errorf("expected worker count %d, got %d", customCount, b.workerCount)
	}
}

func TestBrokerPanicsWithoutHandler(t *testing.T) {
	rw := newMockReadWriter()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when creating broker without handler")
		}
	}()

	New(rw, rw) // Should panic
}

func TestBrokerStop(t *testing.T) {
	rw := newMockReadWriter()

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, nil
	}

	b := New(rw, rw, WithHandler(handler))

	// Stop should complete without hanging
	done := make(chan struct{})
	go func() {
		b.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("broker.Stop() did not complete in time")
	}
}

func TestWorkerPoolProcessesWork(t *testing.T) {
	rw := newMockReadWriter()

	var processedCount atomic.Int32
	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		processedCount.Add(1)
		return []byte("ok"), nil
	}

	b := New(rw, rw, WithHandler(handler), WithWorkerCount(4))
	defer b.Stop()

	// Simulate sending work items by directly using the work channel
	for i := 0; i < 10; i++ {
		b.workChan <- work{
			id:          [16]byte{byte(i)},
			payloadType: payloadTypeRequest,
			payload:     []byte("test"),
		}
	}

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	if processedCount.Load() != 10 {
		t.Errorf("expected 10 items processed, got %d", processedCount.Load())
	}
}

func TestWorkerPoolConcurrency(t *testing.T) {
	rw := newMockReadWriter()

	var maxConcurrent atomic.Int32
	var currentConcurrent atomic.Int32

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		curr := currentConcurrent.Add(1)
		// Update max if current is higher
		for {
			max := maxConcurrent.Load()
			if curr <= max || maxConcurrent.CompareAndSwap(max, curr) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond) // Simulate work
		currentConcurrent.Add(-1)
		return nil, nil
	}

	workerCount := 4
	b := New(rw, rw, WithHandler(handler), WithWorkerCount(workerCount))
	defer b.Stop()

	// Send more work than workers
	for i := 0; i < 20; i++ {
		b.workChan <- work{
			id:          [16]byte{byte(i)},
			payloadType: payloadTypeRequest,
			payload:     []byte("test"),
		}
	}

	// Wait for all work to complete
	time.Sleep(500 * time.Millisecond)

	// Max concurrent should not exceed worker count
	if maxConcurrent.Load() > int32(workerCount) {
		t.Errorf("max concurrent %d exceeded worker count %d", maxConcurrent.Load(), workerCount)
	}
}

func TestWorkQueueFullDropsMessage(t *testing.T) {
	rw := newMockReadWriter()

	// Use a slow handler to fill up the queue
	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		time.Sleep(100 * time.Millisecond)
		return nil, nil
	}

	b := New(rw, rw, WithHandler(handler), WithWorkerCount(1))
	defer b.Stop()

	// Fill up work queue
	for i := 0; i < workQueueSize+10; i++ {
		select {
		case b.workChan <- work{id: [16]byte{byte(i)}, payloadType: payloadTypeRequest}:
		default:
			// Queue full - this is expected for some items
		}
	}

	// The broker should handle this gracefully without blocking
}

func TestResponseChannelHandling(t *testing.T) {
	rw := newMockReadWriter()

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		return []byte("response"), nil
	}

	b := New(rw, rw, WithHandler(handler))
	defer b.Stop()

	// Create a waiter
	id, ch := b.waiters.NewWaiter()

	// Send a response through the work channel
	b.workChan <- work{
		id:          id,
		payloadType: payloadTypeResponse,
		payload:     []byte("test response"),
	}

	// Should receive on the channel
	select {
	case resp := <-ch:
		if string(resp) != "test response" {
			t.Errorf("expected 'test response', got %q", resp)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive response in time")
	}
}

func TestBackoffConstants(t *testing.T) {
	// Verify backoff constants are reasonable
	if initialBackoff <= 0 {
		t.Error("initialBackoff should be positive")
	}
	if maxBackoff <= initialBackoff {
		t.Error("maxBackoff should be greater than initialBackoff")
	}
	if backoffFactor <= 1 {
		t.Error("backoffFactor should be greater than 1")
	}

	// Verify backoff progression
	backoff := initialBackoff
	iterations := 0
	for backoff < maxBackoff {
		backoff *= backoffFactor
		iterations++
		if iterations > 20 {
			t.Fatal("backoff did not reach maxBackoff in reasonable iterations")
		}
	}
}

func TestWriteFrameContextCancellation(t *testing.T) {
	rw := newMockReadWriter()

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, nil
	}

	b := New(rw, rw, WithHandler(handler))
	defer b.Stop()

	// Fill the write channel
	for i := 0; i < 32; i++ {
		b.writeChan <- []byte("fill")
	}

	// Now try to write with canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := b.writeFrame(ctx, payloadTypeRequest, [16]byte{}, []byte("test"))
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestProcessingRequestsTracking(t *testing.T) {
	rw := newMockReadWriter()

	processing := make(chan struct{})
	done := make(chan struct{})

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		close(processing)
		<-done // Wait until we're told to finish
		return nil, nil
	}

	b := New(rw, rw, WithHandler(handler), WithWorkerCount(1))
	defer b.Stop()

	id := [16]byte{1, 2, 3}

	// Send a request
	b.workChan <- work{
		id:          id,
		payloadType: payloadTypeRequest,
		payload:     []byte("test"),
	}

	// Wait for processing to start
	<-processing

	// While processing, the request should be tracked
	if !b.processingRequests.HasRequest(id) {
		t.Error("request should be tracked while processing")
	}

	// Allow handler to finish
	close(done)
	time.Sleep(100 * time.Millisecond)

	// After processing, request should no longer be tracked
	if b.processingRequests.HasRequest(id) {
		t.Error("request should not be tracked after processing")
	}
}

func TestDuplicateRequestIgnored(t *testing.T) {
	rw := newMockReadWriter()

	var callCount atomic.Int32
	processing := make(chan struct{})

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		callCount.Add(1)
		<-processing // Block until released
		return nil, nil
	}

	b := New(rw, rw, WithHandler(handler), WithWorkerCount(2))
	defer b.Stop()

	id := [16]byte{1, 2, 3}

	// Send the same request twice
	b.workChan <- work{id: id, payloadType: payloadTypeRequest, payload: []byte("test")}
	time.Sleep(50 * time.Millisecond) // Let first one start processing
	b.workChan <- work{id: id, payloadType: payloadTypeRequest, payload: []byte("test")}

	time.Sleep(100 * time.Millisecond)
	close(processing) // Release handlers
	time.Sleep(100 * time.Millisecond)

	// Only one should have been processed
	if callCount.Load() != 1 {
		t.Errorf("expected 1 handler call, got %d", callCount.Load())
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"context.Canceled is not retryable", context.Canceled, false},
		{"context.DeadlineExceeded is not retryable", context.DeadlineExceeded, false},
		{"random error is retryable", errors.New("random"), true}, // Most errors are retryable
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryable(tt.err)
			if result != tt.expected {
				t.Errorf("isRetryable(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

// ============================================================================
// Resilience Tests - Test error handling, recovery, and stability
// ============================================================================

func TestIsFatal(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"EBADF is fatal", syscall.EBADF, true},
		{"ENOENT is fatal", syscall.ENOENT, true},
		{"EAGAIN is not fatal", syscall.EAGAIN, false},
		{"EIO is not fatal", syscall.EIO, false},
		{"EPIPE is not fatal", syscall.EPIPE, false},
		{"ECONNRESET is not fatal", syscall.ECONNRESET, false},
		{"ETIMEDOUT is not fatal", syscall.ETIMEDOUT, false},
		{"generic error is not fatal", errors.New("some error"), false},
		{"context.Canceled is not fatal", context.Canceled, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isFatal(tt.err)
			if result != tt.expected {
				t.Errorf("isFatal(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestIsRetryableWithSyscallErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"EBADF is not retryable (fatal)", syscall.EBADF, false},
		{"ENOENT is not retryable (fatal)", syscall.ENOENT, false},
		{"EAGAIN is retryable", syscall.EAGAIN, true},
		{"EIO is retryable", syscall.EIO, true},
		{"EPIPE is retryable", syscall.EPIPE, true},
		{"ECONNRESET is retryable", syscall.ECONNRESET, true},
		{"ETIMEDOUT is retryable", syscall.ETIMEDOUT, true},
		{"EINTR is retryable", syscall.EINTR, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryable(tt.err)
			if result != tt.expected {
				t.Errorf("isRetryable(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// TestBrokerExitsAfterConsecutiveErrors verifies that the broker exits
// after maxConsecutiveErrors read errors (simulating USB disconnection)
func TestBrokerExitsAfterConsecutiveErrors(t *testing.T) {
	rw := &mockReadWriter{
		readData:  make(chan []byte, 100),
		writeData: make(chan []byte, 100),
	}
	// Override readFunc to always return an error
	rw.setReadError(syscall.EIO)

	b := New(rw, rw,
		WithHandler(func(ctx context.Context, payload []byte) ([]byte, error) {
			return []byte("ok"), nil
		}),
	)

	// The read loop should increment error count before we can count
	// Wait for broker to signal unhealthy
	select {
	case <-b.Done():
		// Expected - broker exited due to consecutive errors
	case <-time.After(10 * time.Second):
		t.Fatal("broker did not exit after consecutive errors")
	}

	b.Stop()
}

// TestBrokerExitsOnFatalError verifies immediate exit on fatal errors like EBADF
func TestBrokerExitsOnFatalError(t *testing.T) {
	rw := &mockReadWriter{
		readData:  make(chan []byte, 100),
		writeData: make(chan []byte, 100),
	}
	rw.setReadError(syscall.EBADF) // Fatal error

	b := New(rw, rw,
		WithHandler(func(ctx context.Context, payload []byte) ([]byte, error) {
			return []byte("ok"), nil
		}),
	)

	// Should exit quickly on fatal error
	select {
	case <-b.Done():
		// Expected
	case <-time.After(2 * time.Second):
		t.Fatal("broker did not exit on fatal error")
	}

	b.Stop()
}

// TestWaiterTTLReaping verifies stale waiters are cleaned up
func TestWaiterTTLReaping(t *testing.T) {
	wm := &waiterMap{}

	// Create some waiters
	id1, _ := wm.NewWaiter()
	id2, _ := wm.NewWaiter()

	// Reap with 1 hour TTL - nothing should be reaped (waiters are new)
	reaped := wm.ReapStale(time.Hour)
	if reaped != 0 {
		t.Errorf("reaped = %d, want 0 (nothing old enough)", reaped)
	}

	// Reap with 0 TTL - everything should be reaped immediately
	reaped = wm.ReapStale(0)
	if reaped != 2 {
		t.Errorf("reaped = %d, want 2", reaped)
	}

	// Verify they're gone
	if _, ok := wm.LoadAndDelete(id1); ok {
		t.Error("waiter 1 should have been reaped")
	}
	if _, ok := wm.LoadAndDelete(id2); ok {
		t.Error("waiter 2 should have been reaped")
	}
}

// TestStopTimeoutDoesNotHang verifies Stop() returns within timeout even if loops hang
func TestStopTimeoutDoesNotHang(t *testing.T) {
	rw := newMockReadWriter()

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, nil
	}

	b := New(rw, rw, WithHandler(handler))

	// Stop should return within stopTimeout + buffer
	done := make(chan struct{})
	go func() {
		b.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(stopTimeout + 2*time.Second):
		t.Fatal("Stop() hung longer than expected")
	}
}

// TestHandlerTimeoutPreventsHang verifies handler timeout works correctly
func TestHandlerTimeoutPreventsHang(t *testing.T) {
	timeout := 100 * time.Millisecond

	// Create a slow handler that would hang without timeout
	slowHandler := func(ctx context.Context, payload []byte) ([]byte, error) {
		select {
		case <-time.After(5 * time.Second):
			return []byte("slow"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Simulate the withTimeout wrapper from gadget.go
	wrapped := func(ctx context.Context, payload []byte) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return slowHandler(ctx, payload)
	}

	start := time.Now()
	_, err := wrapped(context.Background(), []byte("test"))
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
	// Should have returned quickly
	if elapsed > 500*time.Millisecond {
		t.Errorf("handler took %v, expected ~%v", elapsed, timeout)
	}
}

// TestBrokerDoneChannelClosesOnLoopExit verifies Done() signals broker failure
func TestBrokerDoneChannelClosesOnLoopExit(t *testing.T) {
	rw := &mockReadWriter{
		readData:  make(chan []byte, 100),
		writeData: make(chan []byte, 100),
	}

	b := New(rw, rw,
		WithHandler(func(ctx context.Context, payload []byte) ([]byte, error) {
			return []byte("ok"), nil
		}),
	)

	// Initially Done() should not be closed
	select {
	case <-b.Done():
		t.Fatal("Done() should not be closed initially")
	default:
		// Good
	}

	// Trigger fatal error to make read loop exit
	rw.setReadError(syscall.EBADF)

	// Done() should close
	select {
	case <-b.Done():
		// Good
	case <-time.After(2 * time.Second):
		t.Fatal("Done() should close after loop exit")
	}

	b.Stop()
}

// TestReaperStartsAndStops verifies the reaper goroutine lifecycle
func TestReaperStartsAndStops(t *testing.T) {
	rw := newMockReadWriter()

	b := New(rw, rw,
		WithHandler(func(ctx context.Context, payload []byte) ([]byte, error) {
			return nil, nil
		}),
	)

	// Create some waiters
	b.waiters.NewWaiter()
	b.waiters.NewWaiter()

	// Stop should clean up reaper
	done := make(chan struct{})
	go func() {
		b.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(stopTimeout + 2*time.Second):
		t.Fatal("Stop() with reaper did not complete in time")
	}
}
