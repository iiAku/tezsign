package watchdog

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestNewReturnsNilWithoutSocket(t *testing.T) {
	// Ensure NOTIFY_SOCKET is not set
	os.Unsetenv("NOTIFY_SOCKET")

	n := New()
	if n != nil {
		t.Error("New() should return nil when NOTIFY_SOCKET is not set")
	}
}

func TestNilNotifierMethodsAreNoOps(t *testing.T) {
	var n *Notifier = nil

	// These should not panic
	if err := n.Ready(); err != nil {
		t.Errorf("Ready() on nil notifier should return nil, got %v", err)
	}
	if err := n.Stopping(); err != nil {
		t.Errorf("Stopping() on nil notifier should return nil, got %v", err)
	}
	if err := n.Ping(); err != nil {
		t.Errorf("Ping() on nil notifier should return nil, got %v", err)
	}
	if err := n.Close(); err != nil {
		t.Errorf("Close() on nil notifier should return nil, got %v", err)
	}

	// StartPinger should return a no-op function
	ctx := context.Background()
	stopFn := n.StartPinger(ctx)
	if stopFn == nil {
		t.Error("StartPinger() on nil notifier should return a non-nil stop function")
	}
	stopFn() // Should not panic
}

func TestWatchdogIntervalReturnsZeroWithoutEnv(t *testing.T) {
	os.Unsetenv("WATCHDOG_USEC")

	interval := WatchdogInterval()
	if interval != 0 {
		t.Errorf("WatchdogInterval() should return 0 without WATCHDOG_USEC, got %v", interval)
	}
}

func TestWatchdogIntervalParsesCorrectly(t *testing.T) {
	tests := []struct {
		usec     string
		expected time.Duration
	}{
		{"60000000", 30 * time.Second},     // 60s -> 30s (half)
		{"30000000", 15 * time.Second},     // 30s -> 15s (half)
		{"10000000", 5 * time.Second},      // 10s -> 5s (half)
		{"1000000", 500 * time.Millisecond}, // 1s -> 500ms (half)
		{"0", 0},
		{"", 0},
		{"invalid", 0},
	}

	for _, tt := range tests {
		os.Setenv("WATCHDOG_USEC", tt.usec)
		interval := WatchdogInterval()
		if interval != tt.expected {
			t.Errorf("WatchdogInterval() with WATCHDOG_USEC=%q = %v, want %v", tt.usec, interval, tt.expected)
		}
	}

	os.Unsetenv("WATCHDOG_USEC")
}

func TestStartPingerWithZeroIntervalIsNoOp(t *testing.T) {
	os.Unsetenv("WATCHDOG_USEC")

	// Create a notifier with an address (even though we can't connect)
	n := &Notifier{addr: "/nonexistent/socket"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopFn := n.StartPinger(ctx)
	if stopFn == nil {
		t.Error("StartPinger() should return a non-nil stop function")
	}
	stopFn() // Should return immediately since interval is 0
}

func TestStartPingerPreventsDuplicates(t *testing.T) {
	// Set a valid interval
	os.Setenv("WATCHDOG_USEC", "1000000") // 1 second
	defer os.Unsetenv("WATCHDOG_USEC")

	n := &Notifier{addr: "/nonexistent/socket"}

	ctx, cancel := context.WithCancel(context.Background())

	// Start first pinger
	stop1 := n.StartPinger(ctx)

	// Try to start second pinger - should return no-op (already running)
	stop2 := n.StartPinger(ctx)

	// Cancel context to stop the pinger goroutine
	cancel()

	// Stop both (should not panic)
	stop1()
	stop2()
}
