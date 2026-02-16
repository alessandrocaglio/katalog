package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"katalog/internal/agent"
	"katalog/internal/config"
	"katalog/internal/metrics"

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

	// Load Initial Config
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if _, err := cfg.Validate(); err != nil {
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

	// Initialize and run the agent
	ag, err := agent.New(&cfg, hostname)
	if err != nil {
		return fmt.Errorf("failed to initialize agent: %w", err)
	}
	ag.Run(ctx)
	return nil
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "katalog",
		Short: "A lightweight, concurrent log forwarding agent.",
		Long: `Katalog is a lightweight, concurrent log forwarding agent written in Go.
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
