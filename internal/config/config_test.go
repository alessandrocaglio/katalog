package config

import (
	"os"
	"strings"
	"testing"
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
			name: "Valid Config with RAW format",
			content: `
poll_interval: "1s"
output_format: "raw"
targets:
  - name: "test-logs"
    paths: ["/tmp/*.log"]
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
			name: "Invalid Output Format",
			content: `
poll_interval: "1s"
output_format: "xml"
targets:
  - name: "logs"
    paths: ["/var/log/app.log"]
`,
			expectError:   true,
			errorContains: "invalid output_format",
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

			cfg, err := Load(path)
			if err != nil {
				// This error is from reading or parsing the file
				if !tt.expectError {
					t.Fatalf("Load() returned unexpected error: %v", err)
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
