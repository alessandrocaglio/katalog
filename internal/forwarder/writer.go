package forwarder

import (
	"bufio"
	"encoding/json"
	"log" // Added for error logging
	"os"
	"time"

	"katalog/internal/models"
)

func WriteLogs(out <-chan models.LogEntry, format string) {
	// Use a buffered writer to reduce syscalls
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()

	encoder := json.NewEncoder(w)

	// Ticker to flush buffer periodically if low traffic
	flushTicker := time.NewTicker(500 * time.Millisecond)
	defer flushTicker.Stop()

	for {
		select {
		case entry, ok := <-out:
			if !ok {
				// Channel closed, flush anything remaining and return
				_ = w.Flush() // Attempt to flush, ignore error on shutdown
				return
			}
			if format == "raw" {
				if _, err := w.WriteString(entry.Event + "\n"); err != nil {
					// Log the error, but continue trying to write next logs
					log.Printf("Error writing raw log to stdout: %v", err)
				}
			} else {
				if err := encoder.Encode(entry); err != nil {
					// Log the error, but continue trying to write next logs
					log.Printf("Error encoding JSON log to stdout: %v", err)
				}
			}
		case <-flushTicker.C:
			if err := w.Flush(); err != nil {
				log.Printf("Error flushing writer buffer: %v", err)
			}
		}
	}
}
