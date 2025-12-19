package broker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"syscall"
	"time"

	"github.com/tez-capital/tezsign/logging"
)

// Optional capabilities (used via type assertions).
type ReadContexter interface {
	ReadContext(ctx context.Context, p []byte) (int, error)
}

type WriteContexter interface {
	WriteContext(ctx context.Context, p []byte) (int, error)
}

type Handler func(ctx context.Context, payload []byte) ([]byte, error)

type options struct {
	bufSize     int
	handler     Handler
	logger      *slog.Logger
	workerCount int
}

type Option func(*options)

func WithBufferSize(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.bufSize = n
		}
	}
}

func WithHandler(h Handler) Option {
	return func(o *options) { o.handler = h }
}

func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithWorkerCount sets the number of worker goroutines for handling requests.
// Default is 8.
func WithWorkerCount(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.workerCount = n
		}
	}
}

// work represents a unit of work for the worker pool
type work struct {
	id          [16]byte
	payloadType payloadType
	payload     []byte
}

type Broker struct {
	r ReadContexter
	w WriteContexter

	stash *stash

	waiters waiterMap
	handler Handler

	writeChan           chan []byte
	workChan            chan work // bounded channel for worker pool
	processingRequests  requestMap[struct{}]
	unconfirmedRequests requestMap[[]byte]

	capacity    int
	workerCount int
	logger      *slog.Logger

	ctx            context.Context
	cancel         context.CancelFunc
	readLoopDone   <-chan struct{}
	writerLoopDone <-chan struct{}
	workersDone    <-chan struct{}
	reaperDone     <-chan struct{}

	// done closes when any critical loop exits (signals broker is no longer healthy)
	done chan struct{}
}

const (
	defaultWorkerCount = 8
	workQueueSize      = 64

	// Backoff constants for retry loops
	initialBackoff = 10 * time.Millisecond
	maxBackoff     = 1 * time.Second
	backoffFactor  = 2

	// Maximum consecutive errors before giving up
	// This prevents tight exit loops while still allowing recovery from transient issues
	maxConsecutiveErrors = 10

	// Waiter TTL: clean up waiters that haven't received responses
	waiterTTL          = 5 * time.Minute
	waiterReapInterval = 30 * time.Second

	// Stop timeout: maximum time to wait for clean shutdown
	stopTimeout = 5 * time.Second
)

func New(r ReadContexter, w WriteContexter, opts ...Option) *Broker {
	o := &options{
		bufSize:     DEFAULT_BROKER_CAPACITY,
		workerCount: defaultWorkerCount,
	}
	for _, fn := range opts {
		fn(o)
	}

	if o.logger == nil {
		o.logger, _ = logging.NewFromEnv()
	}

	if o.handler == nil {
		panic("broker: handler is required (use WithHandler)")
	}

	ctx, cancel := context.WithCancel(context.Background())
	b := &Broker{
		r:           r,
		w:           w,
		capacity:    o.bufSize,
		workerCount: o.workerCount,
		logger:      o.logger,
		handler:     o.handler,

		writeChan:           make(chan []byte, 32),
		workChan:            make(chan work, workQueueSize),
		processingRequests:  NewRequestMap[struct{}](),
		unconfirmedRequests: NewRequestMap[[]byte](),

		stash:  newStash(o.bufSize, o.logger),
		ctx:    ctx,
		cancel: cancel,
	}

	b.done = make(chan struct{})
	b.workersDone = b.startWorkers()
	b.readLoopDone = b.readLoop()
	b.writerLoopDone = b.writerLoop()
	b.reaperDone = b.startReaper()

	// Monitor for loop exits and signal done
	go func() {
		select {
		case <-b.readLoopDone:
			b.logger.Warn("read loop exited, broker unhealthy")
		case <-b.writerLoopDone:
			b.logger.Warn("write loop exited, broker unhealthy")
		case <-b.ctx.Done():
			// Normal shutdown, don't signal unhealthy
			return
		}
		close(b.done)
	}()

	return b
}

// Done returns a channel that closes when the broker becomes unhealthy
// (i.e., when a critical loop exits unexpectedly).
func (b *Broker) Done() <-chan struct{} {
	return b.done
}

// startReaper launches a goroutine that periodically cleans up stale waiters.
func (b *Broker) startReaper() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(waiterReapInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if reaped := b.waiters.ReapStale(waiterTTL); reaped > 0 {
					b.logger.Debug("reaped stale waiters", slog.Int("count", reaped))
				}
			case <-b.ctx.Done():
				return
			}
		}
	}()
	return done
}

// startWorkers launches a fixed pool of worker goroutines
func (b *Broker) startWorkers() <-chan struct{} {
	done := make(chan struct{})

	go func() {
		defer close(done)

		workerDone := make(chan struct{}, b.workerCount)

		for i := 0; i < b.workerCount; i++ {
			go func() {
				defer func() { workerDone <- struct{}{} }()
				for {
					select {
					case w, ok := <-b.workChan:
						if !ok {
							return
						}
						b.handleWork(w)
					case <-b.ctx.Done():
						return
					}
				}
			}()
		}

		// Wait for all workers to finish
		for i := 0; i < b.workerCount; i++ {
			<-workerDone
		}
	}()

	return done
}

// handleWork processes a single work item
func (b *Broker) handleWork(w work) {
	switch w.payloadType {
	case payloadTypeResponse:
		b.logger.Debug("rx resp", slog.String("id", fmt.Sprintf("%x", w.id)), slog.Int("size", len(w.payload)))
		if ch, ok := b.waiters.LoadAndDelete(w.id); ok && ch != nil {
			select {
			case ch <- w.payload:
			default:
				// Channel full or closed, drop response
				b.logger.Warn("response channel full, dropping", slog.String("id", fmt.Sprintf("%x", w.id)))
			}
		}
	case payloadTypeRequest:
		b.logger.Debug("rx req", slog.String("id", fmt.Sprintf("%x", w.id)), slog.Int("size", len(w.payload)))
		if processing := b.processingRequests.HasRequest(w.id); processing {
			b.logger.Debug("duplicate request being processed; ignoring", slog.String("id", fmt.Sprintf("%x", w.id)))
			return
		}
		b.processingRequests.Store(w.id, struct{}{})

		// accept the request immediately
		b.writeFrame(b.ctx, payloadTypeAcceptRequest, w.id, nil)

		if b.handler == nil {
			b.processingRequests.Delete(w.id)
			return
		}
		resp, _ := b.handler(b.ctx, w.payload)
		b.processingRequests.Delete(w.id)

		b.logger.Debug("tx resp", slog.String("id", fmt.Sprintf("%x", w.id)), slog.Int("size", len(resp)))
		_ = b.writeFrame(b.ctx, payloadTypeResponse, w.id, resp)
	case payloadTypeAcceptRequest:
		b.logger.Debug("rx accept", slog.String("id", fmt.Sprintf("%x", w.id)))
		b.unconfirmedRequests.Delete(w.id)
	case payloadTypeRetry:
		b.logger.Debug("rx retry", slog.String("id", fmt.Sprintf("%x", w.id)))
		allUnconfirmed := b.unconfirmedRequests.All()
		for reqID, reqPayload := range allUnconfirmed {
			b.writeFrame(b.ctx, payloadTypeRequest, reqID, reqPayload)
		}
	default:
		b.logger.Warn("unknown type; resync", slog.String("type", fmt.Sprintf("%02x", w.payloadType)), slog.String("id", fmt.Sprintf("%x", w.id)))
	}
}

func (b *Broker) Request(ctx context.Context, payload []byte) ([]byte, [16]byte, error) {
	var id [16]byte
	payloadLen := len(payload)
	if payloadLen > int(^uint32(0)) {
		return nil, id, fmt.Errorf("payload too large")
	}

	if payloadLen > MAX_MESSAGE_PAYLOAD {
		return nil, id, fmt.Errorf("payload exceeds maximum message payload (%d bytes)", MAX_MESSAGE_PAYLOAD)
	}

	id, ch := b.waiters.NewWaiter()
	b.unconfirmedRequests.Store(id, payload)

	b.logger.Debug("tx req", slog.String("id", fmt.Sprintf("%x", id)), slog.Int("size", payloadLen))

	if err := b.writeFrame(ctx, payloadTypeRequest, id, payload); err != nil {
		b.logger.Debug("tx req write failed", slog.String("id", fmt.Sprintf("%x", id)), slog.Any("err", err))
		b.waiters.Delete(id)
		return nil, id, err
	}

	select {
	case resp := <-ch:
		return resp, id, nil
	case <-ctx.Done():
		b.unconfirmedRequests.Delete(id)
		b.waiters.Delete(id)
		return nil, id, ctx.Err()
	case <-b.ctx.Done():
		b.unconfirmedRequests.Delete(id)
		b.waiters.Delete(id)
		return nil, id, io.EOF
	}
}

func (b *Broker) writerLoop() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		backoff := initialBackoff
		consecutiveErrors := 0

		for {
			var data []byte
			select {
			case data = <-b.writeChan:
			case <-b.ctx.Done():
				return
			}

			for {
				select {
				case <-b.ctx.Done():
					return
				default:
				}

				if _, err := b.w.WriteContext(b.ctx, data); err != nil {
					consecutiveErrors++

					// Check if we've hit the error limit
					if consecutiveErrors >= maxConsecutiveErrors {
						b.logger.Error("write loop: too many consecutive errors, exiting",
							slog.Int("errors", consecutiveErrors),
							slog.Any("lastErr", err))
						return
					}

					// Check for fatal errors that should exit immediately
					if isFatal(err) {
						b.logger.Error("write loop: fatal error, exiting", slog.Any("err", err))
						return
					}

					// Context cancellation means shutdown
					if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
						b.logger.Debug("write loop: context done", slog.Any("err", err))
						return
					}

					// All other errors: retry with backoff
					b.logger.Debug("write error, backing off",
						slog.Any("err", err),
						slog.Duration("backoff", backoff),
						slog.Int("consecutiveErrors", consecutiveErrors))

					// Exponential backoff with context check
					select {
					case <-time.After(backoff):
					case <-b.ctx.Done():
						return
					}

					backoff *= backoffFactor
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
					continue
				}
				// Success - reset backoff and error count
				backoff = initialBackoff
				consecutiveErrors = 0
				break
			}
		}
	}()
	return done
}

func (b *Broker) readLoop() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var buf [DEFAULT_READ_BUFFER]byte
		backoff := initialBackoff
		consecutiveErrors := 0

		for {
			select {
			case <-b.ctx.Done():
				return
			default:
			}

			n, err := b.r.ReadContext(b.ctx, buf[:])
			if n > 0 {
				b.stash.Write(buf[:n])
				clear(buf[:n]) // clear buffer after we used it
				b.processStash()
				// Reset backoff and error count on successful read
				backoff = initialBackoff
				consecutiveErrors = 0
			}

			if err != nil {
				consecutiveErrors++

				// Check if we've hit the error limit
				if consecutiveErrors >= maxConsecutiveErrors {
					b.logger.Error("read loop: too many consecutive errors, exiting",
						slog.Int("errors", consecutiveErrors),
						slog.Any("lastErr", err))
					return
				}

				// Check for fatal errors that should exit immediately
				if isFatal(err) {
					b.logger.Error("read loop: fatal error, exiting", slog.Any("err", err))
					return
				}

				// Context cancellation means shutdown
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					b.logger.Debug("read loop: context done", slog.Any("err", err))
					return
				}

				// All other errors: retry with backoff
				b.writeFrame(b.ctx, payloadTypeRetry, [16]byte{}, nil)
				b.logger.Debug("read error, backing off",
					slog.Any("err", err),
					slog.Duration("backoff", backoff),
					slog.Int("consecutiveErrors", consecutiveErrors))

				// Exponential backoff with context check
				select {
				case <-time.After(backoff):
				case <-b.ctx.Done():
					return
				}

				backoff *= backoffFactor
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}
		}
	}()
	return done
}

func (b *Broker) processStash() {
	for {
		id, pt, payload, err := b.stash.ReadPayload()
		switch {
		case errors.Is(err, ErrNoPayloadFound):
			return
		case errors.Is(err, ErrIncompletePayload):
			// Removed runtime.GC() - let Go manage GC naturally
			return
		case errors.Is(err, ErrInvalidPayloadSize):
			continue // resync
		case err != nil:
			b.logger.Warn("bad payload; resync", slog.Any("err", err))
			continue // resync
		}

		// Send work to the worker pool (bounded queue)
		w := work{id: id, payloadType: pt, payload: payload}
		select {
		case b.workChan <- w:
			// Successfully queued
		case <-b.ctx.Done():
			return
		default:
			// Work queue full - log warning but don't block
			b.logger.Warn("work queue full, dropping message",
				slog.String("type", fmt.Sprintf("%02x", pt)),
				slog.String("id", fmt.Sprintf("%x", id)))
		}
	}
}

// writeFrame writes header+payload in one go.
// Uses pooled buffer for payloads <= MAX_POOLED_PAYLOAD and defers Put.
// Larger payloads allocate a right-sized frame (no Put).
func (b *Broker) writeFrame(ctx context.Context, msgType payloadType, id [16]byte, payload []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error("panic in Request", slog.Any("recover", r))
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	frame, err := newMessage(msgType, id, payload)
	if err != nil {
		b.logger.Error("failed to create message frame", slog.Any("error", err))
		return err
	}

	// Non-blocking send with context awareness
	select {
	case b.writeChan <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.ctx.Done():
		return io.EOF
	}
}

func (b *Broker) Stop() {
	b.cancel()

	// Wait for all goroutines with timeout to prevent hanging
	done := make(chan struct{})
	go func() {
		<-b.readLoopDone
		<-b.writerLoopDone
		<-b.reaperDone
		close(b.workChan)
		<-b.workersDone
		close(done)
	}()

	select {
	case <-done:
		// Clean shutdown
	case <-time.After(stopTimeout):
		b.logger.Warn("broker stop timed out, forcing shutdown")
	}
}

// isFatal returns true only for errors that indicate the endpoint is permanently broken
// and cannot recover. For USB gadgets, most errors are transient and should be retried.
func isFatal(err error) bool {
	if err == nil {
		return false
	}

	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.EBADF: // Bad file descriptor - fd is closed/invalid
			return true
		case syscall.ENOENT: // No such file or directory - endpoint removed
			return true
		}
	}

	return false
}

// isRetryable returns true if the error is likely transient and the operation should be retried.
// For USB gadgets, we retry on almost all errors except truly fatal ones (EBADF, ENOENT).
// This is intentionally permissive because USB endpoints can produce many different error
// types during normal operation (disconnect/reconnect, suspend/resume, host resets, etc.).
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Context cancellation is not retryable - it means we're shutting down
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Fatal errors are not retryable
	if isFatal(err) {
		return false
	}

	// Everything else is retryable (USB gadgets can produce many transient error types)
	return true
}
