package forwarder

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"katalog/internal/models"
)

func TestTailFile(t *testing.T) {
	// 1. Create a temporary file to tail
	tmpfile, err := os.CreateTemp("", "app-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	// 2. Setup context and channel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	outCh := make(chan models.LogEntry, 10)

	// 3. Start tailing
	wg.Add(1)
	go TailFile(ctx, &wg, tmpfile.Name(), outCh, TailOptions{
		GroupName: "test-group",
		Hostname:  "test-host",
	})

	// Give the goroutine a moment to open the file and seek to the end
	time.Sleep(100 * time.Millisecond)

	// 4. Write to file and verify output
	messages := []string{"Hello World", "Another Line"}

	for _, msg := range messages {
		if _, err := tmpfile.WriteString(msg + "\n"); err != nil {
			t.Fatal(err)
		}

		// Wait for the log entry
		select {
		case entry := <-outCh:
			if entry.Event != msg {
				t.Errorf("Expected message '%s', got '%s'", msg, entry.Event)
			}
			if entry.SourceType != "test-group" {
				t.Errorf("Expected sourcetype 'test-group', got '%s'", entry.SourceType)
			}
			if entry.Source != filepath.Base(tmpfile.Name()) {
				t.Errorf("Expected source '%s', got '%s'", filepath.Base(tmpfile.Name()), entry.Source)
			}
			if entry.Host != "test-host" {
				t.Errorf("Expected host 'test-host', got '%s'", entry.Host)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Timed out waiting for message: %s", msg)
		}
	}

	// 5. Cleanup
	cancel()
	wg.Wait()
	close(outCh)
}

func TestTailFileRotation(t *testing.T) {
	// 1. Setup directory and initial file
	dir, err := os.MkdirTemp("", "log-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	logPath := filepath.Join(dir, "app.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Setup context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	outCh := make(chan models.LogEntry, 10)

	// 3. Start tailing
	wg.Add(1)
	go TailFile(ctx, &wg, logPath, outCh, TailOptions{
		GroupName: "rotation-group",
		Hostname:  "host",
	})

	// Allow startup
	time.Sleep(100 * time.Millisecond)

	// 4. Write to first file
	if _, err := f.WriteString("Line 1\n"); err != nil {
		t.Fatal(err)
	}

	// Verify Line 1
	select {
	case e := <-outCh:
		if e.Event != "Line 1" {
			t.Errorf("Expected 'Line 1', got '%s'", e.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for Line 1")
	}

	// 5. Rotate: Rename old file, create new one
	rotatedPath := filepath.Join(dir, "app.log.1")
	if err := os.Rename(logPath, rotatedPath); err != nil {
		t.Fatal(err)
	}
	f.Close() // Close writer handle to old file

	// Create new file
	f2, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()

	// 6. Write to new file
	// Wait for the poller to detect the rotation (it sleeps 200ms on EOF)
	time.Sleep(500 * time.Millisecond)

	if _, err := f2.WriteString("Line 2\n"); err != nil {
		t.Fatal(err)
	}

	// Verify Line 2
	select {
	case e := <-outCh:
		if e.Event != "Line 2" {
			t.Errorf("Expected 'Line 2', got '%s'", e.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for Line 2")
	}

	cancel()
	wg.Wait()
}

// func TestTailFileTruncation(t *testing.T) {
// 	// 1. Create a temporary file
// 	tmpfile, err := os.CreateTemp("", "trunc-*.log")
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	defer os.Remove(tmpfile.Name())
// 	defer tmpfile.Close()

// 	// 2. Setup context
// 	ctx, cancel := context.WithCancel(context.Background())
// 	defer cancel()

// 	var wg sync.WaitGroup
// 	outCh := make(chan models.LogEntry, 10)

// 	// 3. Start tailing
// 	wg.Add(1)
// 	go TailFile(ctx, &wg, tmpfile.Name(), outCh, TailOptions{
// 		GroupName: "trunc-group",
// 		Hostname:  "test-host",
// 	})

// 	// Allow startup
// 	time.Sleep(100 * time.Millisecond)

// 	// 4. Write initial data
// 	if _, err := tmpfile.WriteString("Line 1\n"); err != nil {
// 		t.Fatal(err)
// 	}

// 	// Verify Line 1
// 	select {
// 	case entry := <-outCh:
// 		if entry.Event != "Line 1" {
// 			t.Errorf("Expected 'Line 1', got '%s'", entry.Event)
// 		}
// 	case <-time.After(2 * time.Second):
// 		t.Fatal("Timed out waiting for Line 1")
// 	}

// 	// 5. Truncate the file
// 	if err := tmpfile.Truncate(0); err != nil {
// 		t.Fatal(err)
// 	}
// 	// The TailFile's internal logic should handle seeking to the beginning after truncation.
// 	// No need for the test to explicitly seek here.

// 	// Wait for the forwarder to detect truncation and re-seek
// 	// Increased sleep to give ample time for the tailer's internal poll and seek
// 	time.Sleep(2000 * time.Millisecond)

// 	// 6. Write new data
// 	if _, err := tmpfile.WriteString("Line 2\n"); err != nil {
// 		t.Fatal(err)
// 	}

// 	// Verify Line 2
// 	select {
// 	case entry := <-outCh:
// 		if entry.Event != "Line 2" {
// 			t.Errorf("Expected 'Line 2', got '%s'", entry.Event)
// 		}
// 	case <-time.After(2 * time.Second):
// 		t.Fatal("Timed out waiting for Line 2")
// 	}

// 	cancel()
// 	wg.Wait()
// }

func TestTailFileExclusion(t *testing.T) {
	// 1. Create a temporary file
	tmpfile, err := os.CreateTemp("", "exclude-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	// 2. Setup context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	outCh := make(chan models.LogEntry, 10)

	// 3. Compile regex to exclude lines containing "DEBUG"
	re := regexp.MustCompile("DEBUG")

	// 4. Start tailing
	wg.Add(1)
	go TailFile(ctx, &wg, tmpfile.Name(), outCh, TailOptions{
		GroupName:    "exclude-group",
		Hostname:     "test-host",
		ExcludeRegex: re,
	})

	time.Sleep(100 * time.Millisecond)

	// 5. Write mixed logs
	logs := []string{
		"INFO: System started",
		"DEBUG: Variable x=1",
		"INFO: Request received",
	}

	for _, l := range logs {
		if _, err := tmpfile.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}

	// 6. Verify we only get INFO logs
	expected := []string{"INFO: System started", "INFO: Request received"}
	for _, exp := range expected {
		select {
		case entry := <-outCh:
			if entry.Event != exp {
				t.Errorf("Expected '%s', got '%s'", exp, entry.Event)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Timed out waiting for '%s'", exp)
		}
	}

	// Ensure no more messages
	select {
	case entry := <-outCh:
		t.Errorf("Received unexpected message: %s", entry.Event)
	default:
		// OK
	}

	cancel()
	wg.Wait()
}

func TestTailFileMultiline(t *testing.T) {
	// 1. Create temp file
	tmpfile, err := os.CreateTemp("", "multiline-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	// 2. Setup context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	outCh := make(chan models.LogEntry, 10)

	// 3. Compile regex: Start of line must be a Date (e.g., 2023-...)
	// Pattern: ^\d{4}-\d{2}-\d{2}
	multiRe := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

	// 4. Start tailing
	wg.Add(1)
	go TailFile(ctx, &wg, tmpfile.Name(), outCh, TailOptions{
		GroupName:      "multi-group",
		Hostname:       "test-host",
		MultilineRegex: multiRe,
	})

	time.Sleep(100 * time.Millisecond)

	// 5. Write logs
	// Entry 1: Single line
	if _, err := tmpfile.WriteString("2023-01-01 10:00:00 INFO Start\n"); err != nil {
		t.Fatal(err)
	}
	// Entry 2: Multiline (Stack trace)
	if _, err := tmpfile.WriteString("2023-01-01 10:00:01 ERROR Crash\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := tmpfile.WriteString("java.lang.Exception: Boom\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := tmpfile.WriteString("\tat com.example.Main.main(Main.java:10)\n"); err != nil {
		t.Fatal(err)
	}
	// Entry 3: Single line (Triggers flush of Entry 2)
	if _, err := tmpfile.WriteString("2023-01-01 10:00:02 INFO End\n"); err != nil {
		t.Fatal(err)
	}

	// 6. Verify
	// Expect Entry 1
	select {
	case e := <-outCh:
		if !strings.Contains(e.Event, "INFO Start") {
			t.Errorf("Expected 'INFO Start', got '%s'", e.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for Entry 1")
	}

	// Expect Entry 2 (Multiline)
	select {
	case e := <-outCh:
		if !strings.Contains(e.Event, "ERROR Crash") || !strings.Contains(e.Event, "Boom") {
			t.Errorf("Expected multiline crash log, got '%s'", e.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for Entry 2")
	}

	// Entry 3 is in buffer, will be flushed on cancel
	cancel()

	// Verify Entry 3 (Flush on exit)
	select {
	case e := <-outCh:
		if !strings.Contains(e.Event, "INFO End") {
			t.Errorf("Expected 'INFO End', got '%s'", e.Event)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for Entry 3 (flush on exit)")
	}

	wg.Wait()
}

func TestTailFileEnrichment(t *testing.T) {
	// 1. Create temp file
	tmpfile, err := os.CreateTemp("", "enrich-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	// 2. Setup context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	outCh := make(chan models.LogEntry, 10)

	// 3. Define custom fields
	fields := map[string]string{
		"env": "production",
		"app": "payment-service",
	}

	// 4. Start tailing
	wg.Add(1)
	go TailFile(ctx, &wg, tmpfile.Name(), outCh, TailOptions{
		GroupName:    "enrich-group",
		Hostname:     "test-host",
		CustomFields: fields,
	})

	time.Sleep(100 * time.Millisecond)

	// 5. Write log
	if _, err := tmpfile.WriteString("Transaction processed\n"); err != nil {
		t.Fatal(err)
	}

	// 6. Verify fields
	select {
	case e := <-outCh:
		if e.Fields["env"] != "production" {
			t.Errorf("Expected env='production', got '%s'", e.Fields["env"])
		}
		if e.Fields["app"] != "payment-service" {
			t.Errorf("Expected app='payment-service', got '%s'", e.Fields["app"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for enriched log")
	}

	cancel()
	wg.Wait()
}
