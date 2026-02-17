package agent

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"katalog/internal/config"
	"katalog/internal/forwarder"
	"katalog/internal/models"
)

// Package-level variables for the functions we want to make mockable.
// These are initialized with the real implementations by default.
var (
	tailFileFunc  = forwarder.TailFile
	writeLogsFunc = forwarder.WriteLogs
)

type Agent struct {
	cfg        *config.Config
	hostname   string
	logCh      chan models.LogEntry
	tracked    map[string]context.CancelFunc
	wg         sync.WaitGroup
	regexCache map[int]regexPair
}

type regexPair struct {
	exclude   *regexp.Regexp
	multiline *regexp.Regexp
}

func New(cfg *config.Config, hostname string) (*Agent, error) {
	// Pre-compile regexes to avoid compiling them in every loop cycle
	cache := make(map[int]regexPair)
	for i, target := range cfg.Targets {
		var pair regexPair
		var err error
		if target.ExcludePattern != "" {
			if pair.exclude, err = regexp.Compile(target.ExcludePattern); err != nil {
				return nil, fmt.Errorf("invalid exclude_pattern for target '%s': %w", target.Name, err)
			}
		}
		if target.MultilinePattern != "" {
			if pair.multiline, err = regexp.Compile(target.MultilinePattern); err != nil {
				return nil, fmt.Errorf("invalid multiline_pattern for target '%s': %w", target.Name, err)
			}
		}
		cache[i] = pair
	}

	return &Agent{
		cfg:        cfg,
		hostname:   hostname,
		logCh:      make(chan models.LogEntry, 100),
		tracked:    make(map[string]context.CancelFunc),
		regexCache: cache,
	}, nil
}

func (a *Agent) Run(ctx context.Context) {
	// Start the writer goroutine
	var writerWg sync.WaitGroup
	writerWg.Add(1)
	go func() {
		defer writerWg.Done()
		writeLogsFunc(a.logCh, a.cfg.OutputFormat) // Use the mockable function
	}()

	pollDur, _ := time.ParseDuration(a.cfg.PollInterval)
	ticker := time.NewTicker(pollDur)
	defer ticker.Stop()

	log.Println("Log collector started.")

	for {
		a.discover(ctx)

		select {
		case <-ticker.C:
			continue
		case <-ctx.Done():
			log.Println("Shutdown signal received. Cleaning up...")
			for _, cancel := range a.tracked {
				cancel()
			}
			a.wg.Wait()
			close(a.logCh)
			writerWg.Wait()
			log.Println("All collectors stopped. Exiting.")
			return
		}
	}
}

func (a *Agent) discover(ctx context.Context) {
	activeInThisCycle := make(map[string]bool)

	for i, target := range a.cfg.Targets {
		regexes := a.regexCache[i]

		for _, pattern := range target.Paths {
			matches, _ := filepath.Glob(pattern) // Error handling omitted for brevity in glob
			for _, path := range matches {
				activeInThisCycle[path] = true
				if _, ok := a.tracked[path]; !ok {
					fileCtx, cancel := context.WithCancel(ctx)
					a.tracked[path] = cancel
					a.wg.Add(1)

					opts := forwarder.TailOptions{
						GroupName:      target.Name,
						Hostname:       a.hostname,
						ExcludeRegex:   regexes.exclude,
						MultilineRegex: regexes.multiline,
						CustomFields:   target.Fields,
					}

					go tailFileFunc(fileCtx, &a.wg, path, a.logCh, opts) // Use the mockable function
					log.Printf("Started tracking: %s", path)
				}
			}
		}
	}

	// Cleanup untracked files
	for path, cancel := range a.tracked {
		if !activeInThisCycle[path] {
			cancel()
			delete(a.tracked, path)
			log.Printf("Stopped tracking: %s", path)
		}
	}
}
