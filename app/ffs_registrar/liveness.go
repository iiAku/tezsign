package main

import (
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"time"
)

func watchLiveness(sockPath string, ready *atomic.Uint32, l *slog.Logger) {
	for {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			ready.Store(0)
			l.Info("gadget not ready (socket down), retrying", "err", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		l.Info("connected to gadget liveness socket")
		ready.Store(1)
		// Block until the socket dies, then loop.
		_, _ = io.Copy(io.Discard, conn)
		_ = conn.Close()
		ready.Store(0)
		l.Warn("lost liveness socket; marking not ready")
	}
}
