// Package watchdog provides systemd sd_notify integration for watchdog functionality.
//
// This package implements the systemd notify protocol to:
// - Signal READY=1 when the service is fully initialized
// - Send periodic WATCHDOG=1 pings to prevent systemd from killing the service
// - Signal STOPPING=1 during graceful shutdown
//
// The watchdog will only be active if NOTIFY_SOCKET is set by systemd (Type=notify).
// If running outside of systemd or without watchdog, operations are no-ops.
package watchdog

import (
	"context"
	"net"
	"os"
	"sync/atomic"
	"time"
)

// Notifier handles systemd notifications and watchdog pings.
type Notifier struct {
	conn    net.Conn
	addr    string
	running atomic.Bool
}

// New creates a new Notifier. Returns nil if NOTIFY_SOCKET is not set.
func New() *Notifier {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return nil
	}

	return &Notifier{addr: addr}
}

// connect establishes connection to the notify socket if not already connected.
func (n *Notifier) connect() error {
	if n.conn != nil {
		return nil
	}

	conn, err := net.Dial("unixgram", n.addr)
	if err != nil {
		return err
	}
	n.conn = conn
	return nil
}

// send sends a notification message to systemd.
func (n *Notifier) send(msg string) error {
	if err := n.connect(); err != nil {
		return err
	}
	_, err := n.conn.Write([]byte(msg))
	return err
}

// Ready signals to systemd that the service is ready.
// This should be called after all initialization is complete.
func (n *Notifier) Ready() error {
	if n == nil {
		return nil
	}
	return n.send("READY=1")
}

// Stopping signals to systemd that the service is stopping.
// This should be called at the start of graceful shutdown.
func (n *Notifier) Stopping() error {
	if n == nil {
		return nil
	}
	return n.send("STOPPING=1")
}

// Ping sends a watchdog ping to systemd.
// Must be called at least every WatchdogSec/2 to prevent systemd from killing the service.
func (n *Notifier) Ping() error {
	if n == nil {
		return nil
	}
	return n.send("WATCHDOG=1")
}

// WatchdogInterval returns the recommended ping interval based on WATCHDOG_USEC.
// Returns 0 if watchdog is not configured.
func WatchdogInterval() time.Duration {
	usecStr := os.Getenv("WATCHDOG_USEC")
	if usecStr == "" {
		return 0
	}
	var usec int64
	for _, c := range usecStr {
		if c >= '0' && c <= '9' {
			usec = usec*10 + int64(c-'0')
		}
	}
	if usec <= 0 {
		return 0
	}
	// Ping at half the interval to be safe
	return time.Duration(usec) * time.Microsecond / 2
}

// StartPinger starts a goroutine that sends periodic watchdog pings.
// Returns a cleanup function that stops the pinger.
// The pinger respects context cancellation.
func (n *Notifier) StartPinger(ctx context.Context) func() {
	if n == nil {
		return func() {}
	}

	interval := WatchdogInterval()
	if interval == 0 {
		return func() {}
	}

	if !n.running.CompareAndSwap(false, true) {
		// Already running
		return func() {}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				_ = n.Ping()
			case <-ctx.Done():
				return
			}
		}
	}()

	return func() {
		n.running.Store(false)
		<-done
	}
}

// Close cleans up the notifier resources.
func (n *Notifier) Close() error {
	if n == nil || n.conn == nil {
		return nil
	}
	return n.conn.Close()
}
