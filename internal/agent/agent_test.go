package agent

import (
	"context"
	// "errors" // Removed unused import
	"os"
	"path/filepath"
	// "regexp" // Removed unused import
	"reflect" // Added for generic mapKeys
	"strings"
	"sync"
	"testing"
	"time"

	"fmt" // Added for fmt.Sprintf

	"katalog/internal/config"
	"katalog/internal/forwarder"
	"katalog/internal/models"
)

// Helper function to reset mocks to their original implementations after each test
func resetMocks() {
	tailFileFunc = forwarder.TailFile
	writeLogsFunc = forwarder.WriteLogs
}

// TestAgent_New verifies the agent's constructor behavior, including regex compilation.
func TestAgent_New(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *config.Config
		hostname      string
		expectError   bool
		errorContains string
	}{
		{
			name: "Valid Config",
			cfg: &config.Config{
				PollInterval: "1s",
				Targets: []config.Target{
					{Name: "test", Paths: []string{"/tmp/*.log"}},
				},
			},
			hostname:    "test-host",
			expectError: false,
		},
		{
			name: "Invalid Exclude Regex",
			cfg: &config.Config{
				PollInterval: "1s",
				Targets: []config.Target{
					{Name: "bad-regex", Paths: []string{"/tmp/*.log"}, ExcludePattern: "["},
				},
			},
			hostname:      "test-host",
			expectError:   true,
			errorContains: "invalid exclude_pattern",
		},
		{
			name: "Invalid Multiline Regex",
			cfg: &config.Config{
				PollInterval: "1s",
				Targets: []config.Target{
					{Name: "bad-regex", Paths: []string{"/tmp/*.log"}, MultilinePattern: "("},
				},
			},
			hostname:      "test-host",
			expectError:   true,
			errorContains: "invalid multiline_pattern",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ag, err := New(tt.cfg, tt.hostname)
			if (err != nil) != tt.expectError {
				t.Errorf("New() error = %v, expectError %v", err, tt.expectError)
				return
			}
			if err != nil && tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
				t.Errorf("New() error = %v, errorContains %v", err, tt.errorContains)
			}
			if err == nil && ag == nil {
				t.Error("New() returned nil agent with no error")
			}
		})
	}
}

// TestAgent_Run_Shutdown verifies graceful shutdown behavior.
func TestAgent_Run_Shutdown(t *testing.T) {
	t.Cleanup(resetMocks) // Ensure mocks are reset after test

	cfg := &config.Config{
		PollInterval: "10ms", // Fast poll for test
		Targets: []config.Target{
			{Name: "test", Paths: []string{"/tmp/nonexistent/*.log"}}, // Will find no files, but sets up the agent structure
		},
	}
	ag, err := New(cfg, "test-host")
	if err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}

	// Channel to signal when writeLogsFunc is called
	writeLogsCalled := make(chan struct{}, 1)
	// Channel to signal when any tailFileFunc is called
	tailFileCalled := make(chan struct{}, 1)

	// Mock writeLogsFunc
	writeLogsFunc = func(out <-chan models.LogEntry, format string) {
		writeLogsCalled <- struct{}{}
		for range out {
			// Drain channel to allow agent to close it gracefully
		}
	}

	// Mock tailFileFunc - blocks until context is cancelled
	tailFileFunc = func(ctx context.Context, wg *sync.WaitGroup, path string, out chan<- models.LogEntry, opts forwarder.TailOptions) {
		defer wg.Done()
		tailFileCalled <- struct{}{}
		<-ctx.Done() // Block until cancelled by the agent's shutdown process
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure cancel is called on test exit

	var runWg sync.WaitGroup
	runWg.Add(1)
	go func() {
		defer runWg.Done()
		ag.Run(ctx)
	}()

	// Wait for WriteLogs to be called
	select {
	case <-writeLogsCalled:
		t.Log("WriteLogs mock called.")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for WriteLogs mock to be called.")
	}

	// Discover might not call TailFile immediately if no files match glob,
	// but the Run loop will eventually try. For shutdown, we primarily care
	// that Run gracefully exits after its context is cancelled.

	// Trigger shutdown
	cancel()

	// Wait for ag.Run to finish
	select {
	case <-time.After(500 * time.Millisecond): // Give ample time for shutdown
		t.Fatal("Timeout waiting for agent.Run to finish during shutdown.")
	case <-waitChannel(&runWg): // Custom channel to wait for runWg
		t.Log("agent.Run goroutine finished successfully.")
	}
}

// waitChannel converts a WaitGroup to a channel for select statements
func waitChannel(wg *sync.WaitGroup) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		wg.Wait()
		close(ch)
	}()
	return ch
}

// TestAgent_Discover verifies file discovery, tracking, and untracking logic.
func TestAgent_Discover(t *testing.T) {
	t.Cleanup(resetMocks) // Ensure mocks are reset after test

	// Setup a temporary directory for testing glob patterns
	tmpDir, err := os.MkdirTemp("", "agent-test-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create some dummy files
	file1Path := filepath.Join(tmpDir, "app-1.log")
	file2Path := filepath.Join(tmpDir, "app-2.log")
	file3Path := filepath.Join(tmpDir, "app-3.log")
	if _, err := os.Create(file1Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Create(file2Path); err != nil {
		t.Fatal(err)
	}

	// Mock config with glob pattern
	cfg := &config.Config{
		PollInterval: "1s",
		Targets: []config.Target{
			{Name: "app-logs", Paths: []string{filepath.Join(tmpDir, "app-*.log")}},
			{Name: "sys-logs", Paths: []string{filepath.Join(tmpDir, "sys.log")}}, // Initially no match
		},
	}
	ag, err := New(cfg, "test-host")
	if err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}

	// Channels to track calls to the mock TailFile
	tailFileStarted := make(chan string, 5)
	tailFileStopped := make(chan string, 5) // To verify cancellation of tailers

	// Mock tailFileFunc to record calls and block until context is done
	tailFileFunc = func(ctx context.Context, wg *sync.WaitGroup, path string, out chan<- models.LogEntry, opts forwarder.TailOptions) {
		defer wg.Done()
		tailFileStarted <- path
		<-ctx.Done() // Block until cancellation
		tailFileStopped <- path
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run agent.discover directly to control timing
	ag.discover(ctx)

	// Verify initial files were tailed
	expectedStarted := map[string]bool{file1Path: false, file2Path: false}
	for i := 0; i < 2; i++ {
		select {
		case path := <-tailFileStarted:
			if _, ok := expectedStarted[path]; ok {
				expectedStarted[path] = true
			} else {
				t.Errorf("Tailed unexpected file: %s", path)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Timeout waiting for initial files to be tailed")
		}
	}
	// Use reflect-based mapKeys for printing both map types
	if !expectedStarted[file1Path] || !expectedStarted[file2Path] {
		t.Errorf("Not all initial expected files were started. Expected: %v, Actual tracked: %v", mapKeys(expectedStarted), mapKeys(ag.tracked))
	}
	if len(ag.tracked) != 2 {
		t.Errorf("Expected 2 files tracked initially, got %d. Tracked: %v", len(ag.tracked), mapKeys(ag.tracked))
	}

	// Create a new file, discover again - should start tailing the new file
	if _, err := os.Create(file3Path); err != nil {
		t.Fatal(err)
	}
	ag.discover(ctx) // Second discover cycle

	select {
	case path := <-tailFileStarted:
		if path != file3Path {
			t.Errorf("Expected to tail new file %s, got %s", file3Path, path)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for new file to be tailed")
	}
	if len(ag.tracked) != 3 {
		t.Errorf("Expected 3 files tracked after new file, got %d. Tracked: %v", len(ag.tracked), mapKeys(ag.tracked))
	}

	// Remove an existing file, discover again - should stop tailing the removed file
	os.Remove(file1Path)
	ag.discover(ctx) // Third discover cycle

	select {
	case path := <-tailFileStopped:
		if path != file1Path {
			t.Errorf("Expected to stop tailing %s, got %s", file1Path, path)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for removed file to stop tailing")
	}

	// Ensure file1Path is no longer tracked, and others are still tracked
	if _, ok := ag.tracked[file1Path]; ok {
		t.Errorf("File %s should no longer be tracked, but is still in ag.tracked", file1Path)
	}
	if _, ok := ag.tracked[file2Path]; !ok {
		t.Errorf("File %s should still be tracked", file2Path)
	}
	if _, ok := ag.tracked[file3Path]; !ok {
		t.Errorf("File %s should still be tracked", file3Path)
	}
	if len(ag.tracked) != 2 {
		t.Errorf("Expected 2 files tracked after removal, got %d. Tracked: %v", len(ag.tracked), mapKeys(ag.tracked))
	}
}

// mapKeys is a helper to get keys from any map with string keys (for easier debugging output)
func mapKeys(m interface{}) []string {
	v := reflect.ValueOf(m)
	if v.Kind() != reflect.Map {
		return []string{fmt.Sprintf("<not a map: %T>", m)} // Return type info for debugging
	}
	keys := make([]string, 0, v.Len())
	for _, key := range v.MapKeys() {
		if key.Kind() == reflect.String {
			keys = append(keys, key.String())
		}
	}
	return keys
}
