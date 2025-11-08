package main

import (
	"log/slog"
	"net"
	"os"

	"github.com/tez-capital/tezsign/app/gadget/common"
)

// serveReadySocket holds the socket open while the process is healthy.
// Registrar will connect and keep a single connection open.
func serveReadySocket(l *slog.Logger) (cleanup func()) {
	_ = os.Remove(common.ReadySock) // stale
	ln, err := net.Listen("unix", common.ReadySock)
	if err != nil {
		l.Error("ready socket listen", "err", err, "path", common.ReadySock)
		return func() {}
	}
	// world-readable is fine; it's just liveness
	_ = os.Chmod(common.ReadySock, 0666)

	quit := make(chan struct{})
	go func() {
		l.Info("ready socket listening", "path", common.ReadySock)
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-quit:
					return
				default:
					l.Error("ready socket accept", "err", err)
					continue
				}
			}
			// We don’t send anything; keeping the fd open is the signal.
			go func() {
				defer conn.Close()
				// Drain/discard forever; if registrar goes away we’ll just accept next time.
				buf := make([]byte, 1)
				for {
					if _, err := conn.Read(buf); err != nil {
						return
					}
				}
			}()
		}
	}()

	return func() {
		close(quit)
		_ = ln.Close()
		_ = os.Remove(common.ReadySock)
	}
}
