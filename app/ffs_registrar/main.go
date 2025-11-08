package main

import (
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync/atomic"

	"github.com/tez-capital/tezsign/app/gadget/common"
)

func drainEP0Events(ep0 *os.File, ready *atomic.Uint32, l *slog.Logger) {
	buf := make([]byte, evSize)

	for {
		n, err := ep0.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			l.Error("ep0 read events", "err", err)
			return
		}

		if n < evSize {
			l.Warn("ep0 read event too short", "n", n)
			continue
		}

		// [65 90 0 0 0 0 8 0 | 4 | 0 0 0]
		//                      ^ evType
		evType := int(buf[8])
		if evType != evTypeSetup {
			continue
		}

		// [65 90 0 0 0 0 8 0 | 4 | 0 0 0]
		// ^____ request ____^
		req := parseCtrlReq(buf[0:8])
		l.Info("parsed", "type", req.bmRequestType, "request", req.bRequest, "length", req.wLength)
		// Handle our vendor IN request
		if req.bmRequestType == bmReqTypeVendorIn && req.bRequest == vendorReqReady {
			// Prepare reply
			reply := [8]byte{}
			copy(reply[:4], []byte("TZSG"))
			binary.LittleEndian.PutUint16(reply[4:6], protoVersion)
			reply[6] = byte(ready.Load())

			// Respect host's wLength (shorter read is OK)
			wlen := int(req.wLength)
			if wlen > len(reply) {
				wlen = len(reply)
			}
			// Write data stage
			if _, err := ep0.Write(reply[:wlen]); err != nil {
				l.Error("ep0 write vendor reply", "err", err)
			}
			continue
		}
		l.Warn("Unhandled SETUP request, STALLING", "type", req.bmRequestType, "req", req.bRequest)
		// A 0-byte write on an unhandled request is the
		// userspace way to signal a STALL to the kernel.
		if _, err := ep0.Write(nil); err != nil {
			l.Error("ep0 write ZLP/STALL failed", "err", err)
		}
	}
}

func main() {
	l := slog.Default()

	ep0, err := os.OpenFile(Ep0Path, os.O_RDWR, 0)
	if err != nil {
		slog.Error("failed to open ep0", "error", err.Error(), "function", FunctionName, "ffs_root", common.FfsInstanceRoot)
		os.Exit(1)
	}
	defer ep0.Close()

	if _, err := ep0.Write(deviceDescriptors); err != nil {
		slog.Error("failed to write device descriptors", "error", err.Error())
		os.Exit(1)
	}

	if _, err := ep0.Write(deviceStrings); err != nil {
		slog.Error("failed to write device strings", "error", err.Error())
		os.Exit(1)
	}

	// Start watching gadget liveness
	var ready atomic.Uint32
	go watchLiveness(common.ReadySock, &ready, l)

	l.Info("FFS registrar online; handling EP0 control & events")

	drainEP0Events(ep0, &ready, l)
}
