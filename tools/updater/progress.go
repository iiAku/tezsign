package main

import (
	"fmt"
	"io"
	"log/slog"
	"time"
)

type ProgressLogger struct {
	io.Writer
	Written        int64 // Total bytes written so far
	StartTime      time.Time
	Logger         *slog.Logger
	ReportInterval time.Duration
	lastReport     time.Time
	lastWritten    int64
}

// NewProgressLogger creates and initializes a new ProgressLogger.
func NewProgressLogger(w io.Writer, logger *slog.Logger) *ProgressLogger {
	return &ProgressLogger{
		Writer:         w,
		StartTime:      time.Now(),
		Logger:         logger,
		ReportInterval: time.Second, // Report every 1 second
		lastReport:     time.Now(),
	}
}

// Write intercepts the standard Write method to increment the counter and report progress.
func (pl *ProgressLogger) Write(p []byte) (n int, err error) {
	// 1. Write the data to the underlying writer (the io.Pipe writer).
	n, err = pl.Writer.Write(p)

	// 2. Update the counter.
	pl.Written += int64(n)

	// 3. Report progress periodically.
	if time.Since(pl.lastReport) >= pl.ReportInterval {
		pl.reportProgress()
		pl.lastReport = time.Now()
		pl.lastWritten = pl.Written // Reset the baseline for the next interval
	}

	return
}

// reportProgress calculates and logs the current status.
func (pl *ProgressLogger) reportProgress() {
	elapsed := time.Since(pl.lastReport) // Time since last report

	// Calculate data transferred in the last interval
	bytesSinceLastReport := pl.Written - pl.lastWritten

	// Calculate speed
	var speed string
	if elapsed.Seconds() > 0 {
		bytesPerSecond := float64(bytesSinceLastReport) / elapsed.Seconds()
		speed = byteCountToHumanReadable(int64(bytesPerSecond)) + "/s"
	} else {
		speed = "N/A"
	}

	pl.Logger.Info(
		fmt.Sprintf("Copying progress: %s total written. Current speed: %s",
			byteCountToHumanReadable(pl.Written),
			speed,
		),
	)
}

// Helper to convert byte counts to human-readable strings (e.g., MB, GB)
func byteCountToHumanReadable(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
