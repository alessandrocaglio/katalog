package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	"go-log-forwarder/internal/config"
	"go-log-forwarder/internal/forwarder"
	"go-log-forwarder/internal/metrics"
	"go-log-forwarder/internal/models"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
)

func init() {
	metrics.Init()
}

func runForwarder(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	// 1. Setup Context with Signal Handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	tracked := make(map[string]context.CancelFunc)

	// Load Initial Config
	cfg, err := config.Load(configPath)
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
	logCh := make(chan models.LogEntry, 100)

	// Start the writer goroutine
	var writerWg sync.WaitGroup
	writerWg.Add(1)
	go func() {
		defer writerWg.Done()
		forwarder.WriteLogs(logCh)
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
						go forwarder.TailFile(fileCtx, &wg, path, target.Name, hostname, logCh, excludeRegex, multilineRegex, target.Fields)
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
