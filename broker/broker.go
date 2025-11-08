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
	bufSize int
	handler Handler
	logger  *slog.Logger
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

type Broker struct {
	r ReadContexter
	w WriteContexter

	stash *stash

	waiters waiterMap
	handler Handler

	capacity int
	logger   *slog.Logger

	ctx context.Context
}

func New(ctx context.Context, r ReadContexter, w WriteContexter, opts ...Option) *Broker {
	o := &options{
		bufSize: DEFAULT_BROKER_CAPACITY,
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

	b := &Broker{
		r:        r,
		w:        w,
		capacity: o.bufSize,
		logger:   o.logger,
		handler:  o.handler,
		stash:    newStash(o.bufSize, o.logger),
		ctx:      ctx,
	}

	go b.readLoop()
	return b
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
		b.waiters.Delete(id)
		return nil, id, ctx.Err()
	case <-b.ctx.Done():
		b.waiters.Delete(id)
		return nil, id, io.EOF
	}
}

func (b *Broker) readLoop() {
	var buf [DEFAULT_READ_BUFFER]byte
	for {
		n, err := b.r.ReadContext(b.ctx, buf[:])
		if n > 0 {
			b.stash.Write(buf[:n])
			clear(buf[:n]) // clear buffer after we used it
			b.processStash()
		}

		if err != nil {
			if isRetryable(err) {
				b.logger.Debug("read retryable error", slog.Any("err", err))
				time.Sleep(2 * time.Millisecond)
				continue
			}
			b.logger.Debug("read loop exit", slog.Any("err", err))
			return
		}
	}
}

func (b *Broker) processStash() {
	for {
		id, payloadType, payload, err := b.stash.ReadPayload()
		switch {
		case errors.Is(err, ErrNoPayloadFound):
			fallthrough
		case errors.Is(err, ErrIncompletePayload):
			return
		case errors.Is(err, ErrInvalidPayloadSize):
			continue // resync
		case err != nil:
			b.logger.Warn("bad payload; resync", slog.Any("err", err))
			continue // resync
		}

		switch payloadType {
		case payloadTypeResponse:
			b.logger.Debug("rx resp", slog.String("id", fmt.Sprintf("%x", id)), slog.Int("size", len(payload)))

			if ch, ok := b.waiters.LoadAndDelete(id); ok && ch != nil {
				ch <- payload
			}
		case payloadTypeRequest:
			b.logger.Debug("rx req", slog.String("id", fmt.Sprintf("%x", id)), slog.Int("size", len(payload)))

			if b.handler == nil {
				continue
			}

			go func(id [16]byte, pl []byte) {
				resp, _ := b.handler(b.ctx, pl)

				b.logger.Debug("tx resp", slog.String("id", fmt.Sprintf("%x", id)), slog.Int("size", len(resp)))
				_ = b.writeFrame(b.ctx, payloadTypeResponse, id, resp) // Put is deferred inside writeFrame if pooled
			}(id, payload)
		default:
			b.logger.Warn("unknown type; resync", slog.String("type", fmt.Sprintf("%02x", payloadType)), slog.String("id", fmt.Sprintf("%x", id)))
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
		return err
	}
	_, err = b.w.WriteContext(ctx, frame)
	return err
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	// USB endpoints can bounce during (re)bind, host opens, etc.
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.EAGAIN,
			syscall.EINTR,
			syscall.EIO,
			syscall.ENODEV,
			syscall.EPROTO,
			syscall.ESHUTDOWN,
			syscall.EBADMSG,
			syscall.ETIMEDOUT:
			return true
		}
	}
	return false
}
