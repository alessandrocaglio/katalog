package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		filename      string
		expectError   bool
		errorContains string
	}{
		{
			name: "Valid Config",
			content: `
poll_interval: "1s"
targets:
  - name: "test-logs"
    paths:
      - "/tmp/*.log"
`,
			expectError: false,
		},
		{
			name:          "File Not Found",
			filename:      "non_existent_config.yaml",
			expectError:   true,
			errorContains: "no such file or directory",
		},
		{
			name: "Invalid YAML",
			content: `
poll_interval: "1s"
targets: [
  - name: "broken"
`,
			expectError:   true,
			errorContains: "did not find expected node content",
		},
		{
			name: "Invalid Duration",
			content: `
poll_interval: "5x"
targets:
  - name: "logs"
    paths: ["/var/log/app.log"]
`,
			expectError:   true,
			errorContains: "invalid poll_interval",
		},
		{
			name: "No Targets",
			content: `
poll_interval: "1s"
targets: []
`,
			expectError:   true,
			errorContains: "no targets configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			if tt.filename != "" {
				path = tt.filename
			} else {
				tmpfile, err := os.CreateTemp("", "config-*.yaml")
				if err != nil {
					t.Fatal(err)
				}
				defer os.Remove(tmpfile.Name())

				if _, err := tmpfile.Write([]byte(tt.content)); err != nil {
					t.Fatal(err)
				}
				if err := tmpfile.Close(); err != nil {
					t.Fatal(err)
				}
				path = tmpfile.Name()
			}

			cfg, err := loadConfig(path)
			if err != nil {
				// This error is from reading or parsing the file
				if !tt.expectError {
					t.Fatalf("loadConfig() returned unexpected error: %v", err)
				}
				if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Expected load error to contain '%s', but got '%v'", tt.errorContains, err)
				}
				return // End test, as we can't validate a config that failed to load
			}

			// If loading was successful, proceed to validation
			_, err = cfg.Validate()
			if (err != nil) != tt.expectError {
				t.Fatalf("Validate() error expectation mismatch. Expected error: %v, got: %v", tt.expectError, err)
			}
			if err != nil && tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
				t.Errorf("Expected validation error to contain '%s', but got '%v'", tt.errorContains, err)
			}
		})
	}
}

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
	outCh := make(chan LogEntry, 10)

	// 3. Start tailing
	wg.Add(1)
	go tailFile(ctx, &wg, tmpfile.Name(), "test-group", "test-host", outCh, nil, nil, nil)

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
			if entry.Message != msg {
				t.Errorf("Expected message '%s', got '%s'", msg, entry.Message)
			}
			if entry.Group != "test-group" {
				t.Errorf("Expected group 'test-group', got '%s'", entry.Group)
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

func TestWriteLogs(t *testing.T) {
	// 1. Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// 2. Setup channel and data
	outCh := make(chan LogEntry, 1)
	entry := LogEntry{
		Timestamp: "2023-01-01T00:00:00Z",
		Source:    "test.log",
		Group:     "test-group",
		Host:      "localhost",
		Message:   "test message",
	}

	// 3. Run writeLogs in a goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		writeLogs(outCh)
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
	var output LogEntry
	if err := json.Unmarshal(buf.Bytes(), &output); err != nil {
		t.Fatalf("Failed to unmarshal output: %v", err)
	}

	if output.Message != "test message" {
		t.Errorf("Expected message 'test message', got '%s'", output.Message)
	}
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
	outCh := make(chan LogEntry, 10)

	// 3. Start tailing
	wg.Add(1)
	go tailFile(ctx, &wg, logPath, "rotation-group", "host", outCh, nil, nil, nil)

	// Allow startup
	time.Sleep(100 * time.Millisecond)

	// 4. Write to first file
	if _, err := f.WriteString("Line 1\n"); err != nil {
		t.Fatal(err)
	}

	// Verify Line 1
	select {
	case e := <-outCh:
		if e.Message != "Line 1" {
			t.Errorf("Expected 'Line 1', got '%s'", e.Message)
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
		if e.Message != "Line 2" {
			t.Errorf("Expected 'Line 2', got '%s'", e.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for Line 2")
	}

	cancel()
	wg.Wait()
}

func TestTailFileTruncation(t *testing.T) {
	// 1. Create a temporary file
	tmpfile, err := os.CreateTemp("", "trunc-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	// 2. Setup context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	outCh := make(chan LogEntry, 10)

	// 3. Start tailing
	wg.Add(1)
	go tailFile(ctx, &wg, tmpfile.Name(), "trunc-group", "test-host", outCh, nil, nil, nil)

	// Allow startup
	time.Sleep(100 * time.Millisecond)

	// 4. Write initial data
	if _, err := tmpfile.WriteString("Line 1\n"); err != nil {
		t.Fatal(err)
	}

	// Verify Line 1
	select {
	case entry := <-outCh:
		if entry.Message != "Line 1" {
			t.Errorf("Expected 'Line 1', got '%s'", entry.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for Line 1")
	}

	// 5. Truncate the file
	if err := tmpfile.Truncate(0); err != nil {
		t.Fatal(err)
	}
	// Reset our writer offset so we write at the beginning
	if _, err := tmpfile.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	// Wait for the forwarder to detect truncation (poll interval is 200ms)
	time.Sleep(500 * time.Millisecond)

	// 6. Write new data
	if _, err := tmpfile.WriteString("Line 2\n"); err != nil {
		t.Fatal(err)
	}

	// Verify Line 2
	select {
	case entry := <-outCh:
		if entry.Message != "Line 2" {
			t.Errorf("Expected 'Line 2', got '%s'", entry.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for Line 2")
	}

	cancel()
	wg.Wait()
}

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
	outCh := make(chan LogEntry, 10)

	// 3. Compile regex to exclude lines containing "DEBUG"
	re := regexp.MustCompile("DEBUG")

	// 4. Start tailing
	wg.Add(1)
	go tailFile(ctx, &wg, tmpfile.Name(), "exclude-group", "test-host", outCh, re, nil, nil)

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
			if entry.Message != exp {
				t.Errorf("Expected '%s', got '%s'", exp, entry.Message)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Timed out waiting for '%s'", exp)
		}
	}

	// Ensure no more messages
	select {
	case entry := <-outCh:
		t.Errorf("Received unexpected message: %s", entry.Message)
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
	outCh := make(chan LogEntry, 10)

	// 3. Compile regex: Start of line must be a Date (e.g., 2023-...)
	// Pattern: ^\d{4}-\d{2}-\d{2}
	multiRe := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

	// 4. Start tailing
	wg.Add(1)
	go tailFile(ctx, &wg, tmpfile.Name(), "multi-group", "test-host", outCh, nil, multiRe, nil)

	time.Sleep(100 * time.Millisecond)

	// 5. Write logs
	// Entry 1: Single line
	tmpfile.WriteString("2023-01-01 10:00:00 INFO Start\n")
	// Entry 2: Multiline (Stack trace)
	tmpfile.WriteString("2023-01-01 10:00:01 ERROR Crash\n")
	tmpfile.WriteString("java.lang.Exception: Boom\n")
	tmpfile.WriteString("\tat com.example.Main.main(Main.java:10)\n")
	// Entry 3: Single line (Triggers flush of Entry 2)
	tmpfile.WriteString("2023-01-01 10:00:02 INFO End\n")

	// 6. Verify
	// Expect Entry 1
	select {
	case e := <-outCh:
		if !strings.Contains(e.Message, "INFO Start") {
			t.Errorf("Expected 'INFO Start', got '%s'", e.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for Entry 1")
	}

	// Expect Entry 2 (Multiline)
	select {
	case e := <-outCh:
		if !strings.Contains(e.Message, "ERROR Crash") || !strings.Contains(e.Message, "Boom") {
			t.Errorf("Expected multiline crash log, got '%s'", e.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for Entry 2")
	}

	// Entry 3 is in buffer, will be flushed on cancel
	cancel()

	// Verify Entry 3 (Flush on exit)
	select {
	case e := <-outCh:
		if !strings.Contains(e.Message, "INFO End") {
			t.Errorf("Expected 'INFO End', got '%s'", e.Message)
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
	outCh := make(chan LogEntry, 10)

	// 3. Define custom fields
	fields := map[string]string{
		"env": "production",
		"app": "payment-service",
	}

	// 4. Start tailing
	wg.Add(1)
	go tailFile(ctx, &wg, tmpfile.Name(), "enrich-group", "test-host", outCh, nil, nil, fields)

	time.Sleep(100 * time.Millisecond)

	// 5. Write log
	tmpfile.WriteString("Transaction processed\n")

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
