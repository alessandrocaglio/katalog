package forwarder

import (
	"bufio"
	"encoding/json"
	"os"
	"time"

	"go-log-forwarder/internal/models"
)

func WriteLogs(out <-chan models.LogEntry) {
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
			encoder.Encode(entry)
		case <-flushTicker.C:
			w.Flush()
		}
	}
}
