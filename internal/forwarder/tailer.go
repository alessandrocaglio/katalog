package forwarder

import (
	"bufio"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"katalog/internal/metrics"
	"katalog/internal/models"
)

type TailOptions struct {
	GroupName      string
	Hostname       string
	ExcludeRegex   *regexp.Regexp
	MultilineRegex *regexp.Regexp
	CustomFields   map[string]string
}

func TailFile(ctx context.Context, wg *sync.WaitGroup, path string, out chan<- models.LogEntry, opts TailOptions) {
	defer wg.Done()

	file, err := os.Open(path)
	if err != nil {
		metrics.FileErrors.WithLabelValues(path, "open").Inc()
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
		if opts.ExcludeRegex != nil && opts.ExcludeRegex.MatchString(msg) {
			return
		}

		out <- models.LogEntry{
			Time:       time.Now().Unix(),
			Host:       opts.Hostname,
			Source:     filepath.Base(path),
			SourceType: opts.GroupName,
			Event:      msg,
			Fields:     opts.CustomFields,
		}
		metrics.LinesProcessed.WithLabelValues(path, opts.GroupName).Inc()
	}

	// We manage file closing manually to support rotation

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		metrics.FileErrors.WithLabelValues(path, "seek").Inc()
		return
	}
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
							if _, err := file.Seek(0, io.SeekStart); err != nil {
								metrics.FileErrors.WithLabelValues(path, "seek_start").Inc()
								log.Printf("Error seeking to start of file after truncation for %s: %v", path, err)
								file.Close()
								return
							}
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
					metrics.FileErrors.WithLabelValues(path, "read").Inc()
				}
				flushBuffer()
				file.Close()
				return
			}

			// Multiline Logic
			if opts.MultilineRegex != nil {
				// Check if this line starts a new log entry
				if opts.MultilineRegex.MatchString(line) {
					flushBuffer()
				}
				multilineBuffer.WriteString(line)
			} else {
				// Single line mode
				msg := strings.TrimSpace(line)
				if opts.ExcludeRegex != nil && opts.ExcludeRegex.MatchString(msg) {
					continue
				}

				select {
				case out <- models.LogEntry{
					Time:       time.Now().Unix(),
					Host:       opts.Hostname,
					Source:     filepath.Base(path),
					SourceType: opts.GroupName,
					Event:      msg,
					Fields:     opts.CustomFields,
				}:
					metrics.LinesProcessed.WithLabelValues(path, opts.GroupName).Inc()
				case <-ctx.Done():
					file.Close()
					return
				}
			}
		}
	}
}
