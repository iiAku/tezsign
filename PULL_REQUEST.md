# Fix: Resource Leaks, Service Stability & Revival Support

## Summary

This PR addresses critical stability issues that cause the tezsign service to become unresponsive over time, requiring a host machine reboot to recover. It also incorporates and fixes the "revival support" feature from main, enabling automatic recovery when USB is disconnected/reconnected without requiring a service restart.




[Unit]
Description=Force Chrony Sync and Wait
Wants=chronyd.service
After=chronyd.service
Before=your-app.service time-sync.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/wait-for-chrony.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target

---

## Problem Statement

The tezsign service would randomly stop responding:
- Service no longer responds within the 5s timeout
- USB device becomes completely unreachable
- Only solution was to restart the host machine

### Root Cause Analysis

After thorough code review, the following issues were identified:

1. **Critical: Goroutine leak in FFS adapter** - The previous implementation spawned goroutines for I/O operations that never terminated when context was canceled
2. **High: Unbounded goroutine spawning** - Every incoming message spawned a new goroutine with no limit
3. **High: Forced garbage collection** - `runtime.GC()` called in hot path causing latency spikes
4. **Medium: Tight retry loops** - No backoff on errors, causing CPU spinning
5. **Medium: No graceful shutdown** - Resources not properly cleaned up
6. **Medium: Service restart policies** - Services would stop restarting after repeated failures
7. **Bug: Revival support had context cancellation bug** - `cancel` function was never assigned

---

## Changes Made

### 1. FFS Adapter - Poll-Based I/O (`app/gadget/ffs_adapter.go`)

**Before:**
```go
// Previous implementation - LEAKED GOROUTINES
func (r *Reader) ReadContext(ctx context.Context, p []byte) (int, error) {
    readChan := make(chan result, 1)
    go func() {
        n, err := unix.Read(r.fd, p)  // Blocks forever if context canceled
        readChan <- result{n: n, err: err}
    }()
    select {
    case <-ctx.Done():
        return 0, ctx.Err()  // Returns but goroutine still blocked!
    case res := <-readChan:
        return res.n, res.err
    }
}
```

**After:**
```go
// New implementation - Properly cancellable
func (r *Reader) ReadContext(ctx context.Context, p []byte) (int, error) {
    // Set non-blocking mode
    pollFds := []unix.PollFd{{Fd: int32(r.fd), Events: unix.POLLIN}}

    for {
        // Check context before polling
        select {
        case <-ctx.Done():
            return 0, ctx.Err()
        default:
        }

        // Poll with 100ms timeout to allow context checks
        n, err := unix.Poll(pollFds, 100)
        if n == 0 {
            continue  // Timeout - check context and retry
        }

        // Non-blocking read when data available
        return unix.Read(r.fd, p)
    }
}
```

**Key improvements:**
- Uses `poll()` with timeout instead of blocking syscalls
- Checks context every 100ms
- Goroutines properly terminate on context cancellation
- Added `Close()` methods for explicit resource cleanup

---

### 2. Broker - Worker Pool & Backoff (`broker/broker.go`)

#### 2.1 Worker Pool (replaces unbounded goroutines)

**Before:**
```go
// Every message spawned a new goroutine - no limit!
go func(id [16]byte, payloadType payloadType, payload []byte) {
    // handle message
}(id, pt, payload)
```

**After:**
```go
// Fixed pool of 8 workers
const defaultWorkerCount = 8

func (b *Broker) startWorkers() <-chan struct{} {
    for i := 0; i < b.workerCount; i++ {
        go func() {
            for {
                select {
                case w := <-b.workChan:
                    b.handleWork(w)
                case <-b.ctx.Done():
                    return
                }
            }
        }()
    }
}
```

#### 2.2 Exponential Backoff (replaces tight retry loops)

**Before:**
```go
for {
    if _, err := b.w.WriteContext(b.ctx, data); err != nil {
        if isRetryable(err) {
            continue  // Tight loop - CPU spins at 100%
        }
    }
}
```

**After:**
```go
const (
    initialBackoff = 10 * time.Millisecond
    maxBackoff     = 1 * time.Second
    backoffFactor  = 2
)

for {
    if _, err := b.w.WriteContext(b.ctx, data); err != nil {
        if isRetryable(err) {
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
    backoff = initialBackoff  // Reset on success
    break
}
```

#### 2.3 Removed Forced GC

**Before:**
```go
case errors.Is(err, ErrIncompletePayload):
    runtime.GC()  // Forces stop-the-world GC on every incomplete read
    return
```

**After:**
```go
case errors.Is(err, ErrIncompletePayload):
    // Let Go manage GC naturally
    return
```

---

### 3. Revival Support & Graceful Shutdown (`app/gadget/gadget.go`)

The gadget now implements a **revival loop** that:
- Waits for the enabled socket from `ffs_registrar`
- Starts brokers when USB is enabled
- Stops brokers when USB is disabled (detected via socket close)
- Automatically restarts when USB is re-enabled
- Handles SIGTERM/SIGINT for clean process shutdown

**Architecture:**
```go
func run(l *slog.Logger) error {
    // Setup keystore...

    // Signal handling for clean shutdown
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

    // Revival loop
    for {
        // Check for shutdown signal
        select {
        case sig := <-sigCh:
            l.Info("Received shutdown signal")
            return nil
        default:
        }

        // Wait for enabled socket
        enabled, err := net.Dial("unix", common.EnabledSock)
        if err != nil {
            time.Sleep(100 * time.Millisecond)
            continue
        }

        ctx, cancel := context.WithCancel(context.Background())

        // Monitor for disable or shutdown
        go func() {
            select {
            case <-socketClosed:
                l.Warn("gadget disabled; stopping brokers")
            case <-sigCh:
                l.Info("Received shutdown signal during operation")
            }
            cancel()
        }()

        runBrokers(ctx, fs, kr, l)  // Blocks until ctx cancelled
    }
}
```

---

### 4. Bug Fixes in Revival Support (`app/ffs_registrar/liveness.go`)

The "support revival" commit from main had several bugs that are now fixed:

#### 4.1 Fixed: Context Cancel Never Assigned

**Before (broken):**
```go
func runEnabledWatcher(enabled <-chan bool, sockPath string, l *slog.Logger) {
    var cancel context.CancelFunc  // Declared but never assigned!
    for en := range enabled {
        if en && !isEnabled {
            isEnabled = true
            ctx := context.Background()  // No cancel returned!
            closeCompletedChan = serveEnabled(ctx, sockPath, l)
        } else if !en && isEnabled {
            if cancel != nil {  // Always nil - never works!
                cancel()
            }
        }
    }
}
```

**After (fixed):**
```go
func runEnabledWatcher(enabled <-chan bool, sockPath string, l *slog.Logger) {
    var cancel context.CancelFunc
    for en := range enabled {
        if en && !isEnabled {
            isEnabled = true
            var ctx context.Context
            ctx, cancel = context.WithCancel(context.Background())  // Properly assigned!
            closeCompletedChan = serveEnabled(ctx, sockPath, l)
        } else if !en && isEnabled {
            isEnabled = false  // Reset state
            if cancel != nil {
                cancel()
                cancel = nil
                <-closeCompletedChan
            }
            _ = os.Remove(sockPath)  // Clean up socket file
        }
    }
}
```

#### 4.2 Fixed: Connection Tracking for Clean Shutdown

**Before:**
```go
// Connections spawned goroutines that never terminated on shutdown
go func(c net.Conn) {
    defer c.Close()
    io.Copy(io.Discard, c)  // Blocks until connection dies naturally
}(conn)
```

**After:**
```go
// Track connections and close them on shutdown
var connMu sync.Mutex
conns := make(map[net.Conn]struct{})

go func() {
    <-ctx.Done()
    ln.Close()
    // Close all active connections
    connMu.Lock()
    for c := range conns {
        c.Close()
    }
    connMu.Unlock()
}()

// Register/deregister connections
connMu.Lock()
conns[conn] = struct{}{}
connMu.Unlock()

go func(c net.Conn) {
    defer func() {
        c.Close()
        connMu.Lock()
        delete(conns, c)
        connMu.Unlock()
    }()
    io.Copy(io.Discard, c)
}(conn)
```

#### 4.3 Fixed: Stale Socket File Handling

Added cleanup of stale socket files:
- Before listening: `os.Remove(sockPath)` to handle stale files from crashes
- On disable: `os.Remove(sockPath)` to clean up

---

### 5. Liveness Socket (`app/gadget/liveness.go`)

**Before:**
```go
// Each connection spawned a goroutine that never terminated
go func() {
    defer conn.Close()
    buf := make([]byte, 1)
    for {
        conn.Read(buf)  // Blocks forever
    }
}()
```

**After:**
```go
// Single active connection, proper cleanup
func serveReadySocket(l *slog.Logger) (cleanup func()) {
    var currentConn net.Conn

    for {
        conn, err := ln.Accept()

        // Close previous connection (only one active at a time)
        if currentConn != nil {
            currentConn.Close()
        }
        currentConn = conn

        go func(c net.Conn) {
            for {
                c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
                _, err := c.Read(buf)
                if err != nil {
                    select {
                    case <-quit:
                        return  // Clean exit
                    default:
                    }
                    if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
                        continue  // Expected timeout
                    }
                    return
                }
            }
        }(conn)
    }
}
```

---

### 6. Systemd Service Resilience

#### `tezsign.service`

| Setting | Before | After | Purpose |
|---------|--------|-------|---------|
| `Restart` | `on-failure` | `always` | Restart on any exit |
| `StartLimitIntervalSec` | (missing) | `0` | Never stop restarting |
| `RemainAfterExit` | `yes` | (removed) | Incorrect for daemons |
| `StandardOutput` | `null` | `journal` | Enable debugging |
| `LimitNOFILE` | (missing) | `4096` | Prevent fd exhaustion |
| `LimitNPROC` | (missing) | `256` | Limit processes |

#### `ffs_registrar.service`

| Setting | Before | After | Purpose |
|---------|--------|-------|---------|
| `StartLimitIntervalSec` | (missing) | `0` | Never stop restarting |
| `RemainAfterExit` | `yes` | (removed) | Incorrect for daemons |

#### `attach-gadget-dev.service`

| Setting | Before | After | Purpose |
|---------|--------|-------|---------|
| `Restart` | `on-failure` | `always` | Restart on any exit |
| `StartLimitIntervalSec` | (missing) | `0` | Never stop restarting |

---

## Test Coverage

New test files added to verify the fixes:

### `app/gadget/ffs_adapter_test.go` (8 tests)
- `TestReaderContextCancellation` - Verifies read terminates on context cancel
- `TestReaderSuccessfulRead` - Verifies normal read operation
- `TestWriterContextCancellation` - Verifies write terminates on context cancel
- `TestWriterSuccessfulWrite` - Verifies normal write operation
- `TestReaderClose` - Verifies close prevents further reads
- `TestWriterClose` - Verifies close prevents further writes
- `TestReaderNoGoroutineLeak` - Verifies no goroutine accumulation
- `TestWriterEmptyWrite` - Edge case for empty writes

### `broker/broker_test.go` (13 tests)
- `TestBrokerCreation` - Basic broker initialization
- `TestBrokerWithCustomWorkerCount` - Custom worker pool size
- `TestBrokerPanicsWithoutHandler` - Validates required handler
- `TestBrokerStop` - Clean shutdown
- `TestWorkerPoolProcessesWork` - Work items processed correctly
- `TestWorkerPoolConcurrency` - Concurrent workers limited
- `TestWorkQueueFullDropsMessage` - Graceful handling of full queue
- `TestResponseChannelHandling` - Response routing
- `TestBackoffConstants` - Backoff values are reasonable
- `TestWriteFrameContextCancellation` - Write respects context
- `TestProcessingRequestsTracking` - Request deduplication
- `TestDuplicateRequestIgnored` - Duplicate handling
- `TestIsRetryable` - Error classification

### `app/gadget/liveness_test.go` (8 tests)
- `TestServeReadySocketCreatesSocket` - Socket file created
- `TestServeReadySocketAcceptsConnection` - Connections accepted
- `TestServeReadySocketOnlyOneActiveConnection` - Single connection enforced
- `TestServeReadySocketCleanup` - Clean shutdown
- `TestServeReadySocketHandlesMultipleReconnects` - Reconnection handling
- `TestServeReadySocketNoGoroutineLeak` - No goroutine accumulation
- `TestServeReadySocketPermissions` - Correct file permissions
- `TestServeReadySocketRemovesStaleSocket` - Stale socket cleanup

---

## Device Compatibility

These changes have been designed to be compatible with all target devices:

| Device | Architecture | RAM | Status |
|--------|-------------|-----|--------|
| Raspberry Pi Zero 2W | ARM Cortex-A53 | 512MB | Compatible |
| Raspberry Pi 4 | ARM Cortex-A72 | 1-8GB | Compatible |
| Radxa Zero 3 | ARM (RK3566) | 1-8GB | Compatible |

### Considerations for Embedded Devices

1. **Resource limits** - Conservative values (`LimitNOFILE=4096`, `LimitNPROC=256`) suitable for RPi Zero 2W
2. **No security hardening** - Removed `ProtectSystem=strict` which could conflict with custom `/app` and `/data` mount points
3. **Worker pool size** - Default 8 workers is reasonable for quad-core ARM processors
4. **Backoff timings** - Start at 10ms, max 1s - balances responsiveness with resource usage

---

## Files Changed

```
Modified:
  app/gadget/ffs_adapter.go              - Poll-based I/O
  app/gadget/gadget.go                   - Revival loop + signal handling
  app/gadget/liveness.go                 - Connection management
  app/ffs_registrar/liveness.go          - Bug fixes for enabled socket
  broker/broker.go                       - Worker pool + backoff
  tools/builder/assets/tezsign.service   - Restart policies
  tools/builder/assets/ffs_registrar.service - Restart policies
  tools/builder/assets/attach-gadget-dev.service - Restart policies

Added:
  app/gadget/ffs_adapter_test.go         (new)
  app/gadget/liveness_test.go            (new)
  broker/broker_test.go                  (new)
```

---

## Testing

```bash
# Build
go build ./...  # SUCCESS

# Static analysis
go vet ./...    # SUCCESS (no issues)

# Unit tests
go test ./...   # 29 tests PASSED
```

---

## Breaking Changes

None. All changes are backward compatible.

---

## Deployment Notes

After deploying this update:

1. Services will automatically restart on any failure (not just error exits)
2. USB disconnect/reconnect is now handled automatically (no service restart needed)
3. Logs are now captured to systemd journal - use `journalctl -u tezsign` to view
4. If USB issues occur, the service will use exponential backoff instead of spinning

To view logs on the device:
```bash
journalctl -u tezsign -f        # Follow tezsign logs
journalctl -u ffs_registrar -f  # Follow registrar logs
```
