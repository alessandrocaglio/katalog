package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Global metrics
var (
	linesProcessed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "log_forwarder_lines_total",
			Help: "Total number of log lines processed per file",
		},
		[]string{"path", "group"},
	)
	fileErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "log_forwarder_file_errors_total",
			Help: "Total number of file errors",
		},
		[]string{"path", "error_type"},
	)
)

func init() {
	prometheus.MustRegister(linesProcessed, fileErrors)
}

type Config struct {
	PollInterval string   `yaml:"poll_interval"`
	Targets      []Target `yaml:"targets"`
}

// Validate checks the configuration and returns the parsed poll duration.
func (c *Config) Validate() (time.Duration, error) {
	if c.PollInterval == "" {
		return 0, fmt.Errorf("poll_interval must be set")
	}
	pollDur, err := time.ParseDuration(c.PollInterval)
	if err != nil {
		return 0, fmt.Errorf("invalid poll_interval: %w", err)
	}
	if len(c.Targets) == 0 {
		return 0, fmt.Errorf("no targets configured")
	}
	return pollDur, nil
}

type Target struct {
	Name             string            `yaml:"name"`
	Paths            []string          `yaml:"paths"`
	ExcludePattern   string            `yaml:"exclude_pattern,omitempty"`
	MultilinePattern string            `yaml:"multiline_pattern,omitempty"`
	Fields           map[string]string `yaml:"fields,omitempty"`
}

type LogEntry struct {
	Timestamp string            `json:"timestamp"`
	Source    string            `json:"source"`
	Group     string            `json:"group"`
	Host      string            `json:"host"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
}

func tailFile(ctx context.Context, wg *sync.WaitGroup, path string, groupName string, hostname string, out chan<- LogEntry, excludeRegex *regexp.Regexp, multilineRegex *regexp.Regexp, customFields map[string]string) {
	defer wg.Done()

	file, err := os.Open(path)
	if err != nil {
		fileErrors.WithLabelValues(path, "open").Inc()
		return
	}

	var multilineBuffer strings.Builder

	// Helper to flush multiline buffer
	flushBuffer := func() {
		if multilineBuffer.Len() == 0 {
			return
		}
		msg := strings.TrimSpace(multilineBuffer.String())
		multilineBuffer.Reset()

		if msg == "" {
			return
		}
		if excludeRegex != nil && excludeRegex.MatchString(msg) {
			return
		}

		out <- LogEntry{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Source:    filepath.Base(path),
			Group:     groupName,
			Host:      hostname,
			Message:   msg,
			Fields:    customFields,
		}
		linesProcessed.WithLabelValues(path, groupName).Inc()
	}

	// We manage file closing manually to support rotation

	file.Seek(0, io.SeekEnd)
	fi, err := file.Stat()
	if err != nil {
		file.Close()
		return
	}
	reader := bufio.NewReader(file)

	for {
		select {
		case <-ctx.Done():
			log.Printf("Shutting down collector for: %s", path)
			flushBuffer()
			file.Close()
			return
		default:
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					// Check for rotation
					if newFi, err := os.Stat(path); err == nil {
						if !os.SameFile(fi, newFi) {
							log.Printf("File rotation detected: %s", path)
							flushBuffer() // Flush any partial/complete logs from old file
							newFile, err := os.Open(path)
							if err == nil {
								file.Close()
								file = newFile
								fi = newFi
								reader = bufio.NewReader(file)
								continue
							}
						} else if newFi.Size() < fi.Size() {
							// Handle truncation (inode same, but size decreased)
							log.Printf("File truncation detected: %s", path)
							multilineBuffer.Reset() // Discard partial buffer on truncation
							file.Seek(0, io.SeekStart)
							fi = newFi
							reader = bufio.NewReader(file)
							continue
						}
					}
					// Update file info to current state for next comparison
					if stat, err := file.Stat(); err == nil {
						fi = stat
					}
					// Smaller sleep for better responsiveness
					time.Sleep(200 * time.Millisecond)
					continue
				}
				if err != io.EOF {
					fileErrors.WithLabelValues(path, "read").Inc()
				}
				flushBuffer()
				file.Close()
				return
			}

			// Multiline Logic
			if multilineRegex != nil {
				// Check if this line starts a new log entry
				if multilineRegex.MatchString(line) {
					flushBuffer()
				}
				multilineBuffer.WriteString(line)
			} else {
				// Single line mode
				msg := strings.TrimSpace(line)
				if excludeRegex != nil && excludeRegex.MatchString(msg) {
					continue
				}

				select {
				case out <- LogEntry{
					Timestamp: time.Now().UTC().Format(time.RFC3339),
					Source:    filepath.Base(path),
					Group:     groupName,
					Host:      hostname,
					Message:   msg,
					Fields:    customFields,
				}:
					linesProcessed.WithLabelValues(path, groupName).Inc()
				case <-ctx.Done():
					file.Close()
					return
				}
			}
		}
	}
}

func writeLogs(out <-chan LogEntry) {
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

func runForwarder(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	// 1. Setup Context with Signal Handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	tracked := make(map[string]context.CancelFunc)

	// Load Initial Config
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	pollDur, err := cfg.Validate()
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("could not get hostname: %w", err)
	}

	// Start Metrics Server
	metricsAddr, _ := cmd.Flags().GetString("metrics-addr")
	if metricsAddr != "" {
		go func() {
			http.Handle("/metrics", promhttp.Handler())
			log.Printf("Metrics server listening on %s", metricsAddr)
			log.Printf("Error starting metrics server: %v", http.ListenAndServe(metricsAddr, nil))
		}()
	}

	log.Println("Log collector started. Press Ctrl+C to stop.")

	// Channel for centralizing log output
	logCh := make(chan LogEntry, 100)

	// Start the writer goroutine
	var writerWg sync.WaitGroup
	writerWg.Add(1)
	go func() {
		defer writerWg.Done()
		writeLogs(logCh)
	}()

	// Main Loop
	ticker := time.NewTicker(pollDur)
	defer ticker.Stop()

	for {
		activeInThisCycle := make(map[string]bool)

		for _, target := range cfg.Targets {
			var excludeRegex *regexp.Regexp
			if target.ExcludePattern != "" {
				var err error
				if excludeRegex, err = regexp.Compile(target.ExcludePattern); err != nil {
					log.Printf("Error compiling exclude_pattern for target '%s': %v", target.Name, err)
					continue
				}
			}

			var multilineRegex *regexp.Regexp
			if target.MultilinePattern != "" {
				var err error
				if multilineRegex, err = regexp.Compile(target.MultilinePattern); err != nil {
					log.Printf("Error compiling multiline_pattern for target '%s': %v", target.Name, err)
					continue
				}
			}

			for _, pattern := range target.Paths {
				matches, err := filepath.Glob(pattern)
				if err != nil {
					log.Printf("Error matching glob pattern '%s': %v", pattern, err)
					continue
				}
				for _, path := range matches {
					activeInThisCycle[path] = true
					if _, ok := tracked[path]; !ok {
						// Create a sub-context for this specific file
						fileCtx, cancel := context.WithCancel(ctx)
						tracked[path] = cancel

						wg.Add(1)
						go tailFile(fileCtx, &wg, path, target.Name, hostname, logCh, excludeRegex, multilineRegex, target.Fields)
						log.Printf("Started tracking: %s", path)
					}
				}
			}
		}

		// Cleanup untracked files
		for path, cancel := range tracked {
			if !activeInThisCycle[path] {
				cancel()
				delete(tracked, path)
				log.Printf("Stopped tracking: %s", path)
			}
		}

		select {
		case <-ticker.C:
			continue
		case <-ctx.Done():
			// 2. Triggered on SIGTERM/SIGINT
			log.Println("Shutdown signal received. Cleaning up...")

			// Cancel all individual file contexts
			for _, cancel := range tracked {
				cancel()
			}

			// Wait for all goroutines to finish their defer wg.Done()
			wg.Wait()

			// Close channel and wait for writer to finish
			close(logCh)
			writerWg.Wait()

			log.Println("All collectors stopped. Exiting.")
			return nil
		}
	}
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "log-forwarder",
		Short: "A lightweight, concurrent log forwarding agent.",
		Long: `Go Log Forwarder is a lightweight, concurrent log forwarding agent written in Go.
It monitors multiple log files defined by glob patterns, enriches the log lines with metadata, and outputs them as JSON to stdout.`,
		RunE: runForwarder,
	}

	rootCmd.PersistentFlags().String("config", "config.yaml", "path to config file")
	rootCmd.PersistentFlags().String("metrics-addr", ":8080", "address to bind metrics server (e.g. :8080)")

	if err := rootCmd.Execute(); err != nil {
		// Cobra prints the error, so we just need to exit.
		os.Exit(1)
	}
}

func loadConfig(path string) (Config, error) {
	yamlFile, err := os.ReadFile(path)
	var cfg Config
	if err != nil {
		return cfg, err
	}
	err = yaml.Unmarshal(yamlFile, &cfg)
	return cfg, err
}
