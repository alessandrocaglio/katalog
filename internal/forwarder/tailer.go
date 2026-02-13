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

	"go-log-forwarder/internal/metrics"
	"go-log-forwarder/internal/models"
)

func TailFile(ctx context.Context, wg *sync.WaitGroup, path string, groupName string, hostname string, out chan<- models.LogEntry, excludeRegex *regexp.Regexp, multilineRegex *regexp.Regexp, customFields map[string]string) {
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
		if excludeRegex != nil && excludeRegex.MatchString(msg) {
			return
		}

		out <- models.LogEntry{
			Time:       time.Now().Unix(),
			Host:       hostname,
			Source:     filepath.Base(path),
			SourceType: groupName,
			Event:      msg,
			Fields:     customFields,
		}
		metrics.LinesProcessed.WithLabelValues(path, groupName).Inc()
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
					metrics.FileErrors.WithLabelValues(path, "read").Inc()
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
				case out <- models.LogEntry{
					Time:       time.Now().Unix(),
					Host:       hostname,
					Source:     filepath.Base(path),
					SourceType: groupName,
					Event:      msg,
					Fields:     customFields,
				}:
					metrics.LinesProcessed.WithLabelValues(path, groupName).Inc()
				case <-ctx.Done():
					file.Close()
					return
				}
			}
		}
	}
}
