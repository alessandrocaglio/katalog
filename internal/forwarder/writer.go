package forwarder

import (
	"bufio"
	"encoding/json"
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
				return
			}
			if format == "raw" {
				w.WriteString(entry.Event + "\n")
			} else {
				encoder.Encode(entry)
			}
		case <-flushTicker.C:
			w.Flush()
		}
	}
}
