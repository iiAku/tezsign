// Package health provides low-overhead health monitoring for blockchain signing services.
//
// Design principles:
// - Zero allocation on the signing path (atomic ops only)
// - No locks on the signing path
// - No I/O on the signing path
// - Overhead: ~10ns per signing operation (2 atomic ops)
package health

import (
	"runtime"
	"sync/atomic"
	"time"
)

// Monitor tracks service health with minimal overhead.
type Monitor struct {
	lastActivity   atomic.Int64  // Unix timestamp of last activity
	requestCount   atomic.Uint64 // Total request counter
	goroutineLimit int           // Max allowed goroutines
}

// NewMonitor creates a new health monitor.
// goroutineLimit is the maximum number of goroutines allowed (0 = no limit).
func NewMonitor(goroutineLimit int) *Monitor {
	m := &Monitor{
		goroutineLimit: goroutineLimit,
	}
	m.lastActivity.Store(time.Now().Unix())
	return m
}

// RecordActivity should be called after each signing operation completes.
// This is the hot path - uses only atomic operations (~10ns overhead).
func (m *Monitor) RecordActivity() {
	m.lastActivity.Store(time.Now().Unix())
	m.requestCount.Add(1)
}

// LastActivity returns the Unix timestamp of the last recorded activity.
func (m *Monitor) LastActivity() time.Time {
	return time.Unix(m.lastActivity.Load(), 0)
}

// RequestCount returns the total number of requests processed.
func (m *Monitor) RequestCount() uint64 {
	return m.requestCount.Load()
}

// SecondsSinceActivity returns seconds since last activity.
func (m *Monitor) SecondsSinceActivity() int64 {
	return time.Now().Unix() - m.lastActivity.Load()
}

// IsHealthy performs health checks. This is NOT on the signing path.
// Call this from a background goroutine (every 10s recommended).
func (m *Monitor) IsHealthy() bool {
	// Check goroutine count (only if limit is set)
	if m.goroutineLimit > 0 && runtime.NumGoroutine() > m.goroutineLimit {
		return false
	}
	return true
}

// GoroutineCount returns the current number of goroutines.
// Only call from background health checks, not on signing path.
func (m *Monitor) GoroutineCount() int {
	return runtime.NumGoroutine()
}
