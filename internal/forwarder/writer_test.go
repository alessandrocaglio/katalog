package forwarder

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sync"
	"testing"

	"katalog/internal/models"
)

func TestWriteLogs(t *testing.T) {
	// 1. Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// 2. Setup channel and data
	outCh := make(chan models.LogEntry, 1)
	entry := models.LogEntry{
		Time:       1672531200,
		Source:     "test.log",
		SourceType: "test-group",
		Host:       "localhost",
		Event:      "test message",
	}

	// 3. Run writeLogs in a goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		WriteLogs(outCh, "json")
	}()

	// 4. Send data and close
	outCh <- entry
	close(outCh)
	wg.Wait()

	// 5. Restore stdout and read output
	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)

	// 6. Verify JSON
	var output models.LogEntry
	if err := json.Unmarshal(buf.Bytes(), &output); err != nil {
		t.Fatalf("Failed to unmarshal output: %v", err)
	}

	if output.Event != "test message" {
		t.Errorf("Expected event 'test message', got '%s'", output.Event)
	}
}

func TestWriteLogsRaw(t *testing.T) {
	// 1. Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// 2. Setup channel and data
	outCh := make(chan models.LogEntry, 1)
	entry := models.LogEntry{
		Time:       1672531200,
		Source:     "test.log",
		SourceType: "test-group",
		Host:       "localhost",
		Event:      "raw message",
	}

	// 3. Run writeLogs in a goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		WriteLogs(outCh, "raw")
	}()

	// 4. Send data and close
	outCh <- entry
	close(outCh)
	wg.Wait()

	// 5. Restore stdout and read output
	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)

	if buf.String() != "raw message\n" {
		t.Errorf("Expected 'raw message\\n', got '%s'", buf.String())
	}
}
