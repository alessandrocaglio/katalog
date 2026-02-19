# Katalog

![CI](https://github.com/alessandrocaglio/katalog/actions/workflows/ci.yaml/badge.svg)

**A lightweight, concurrent log forwarding agent written in Go.**

Katalog monitors log files, enriches them with metadata, and streams them as structured JSON to `stdout`. It's designed to be a simple, efficient, and reliable building block for your logging pipeline.

### Why "Katalog"?

It's a pun! This tool reads files (like `cat`) and organizes them into a stream (like a catalog). **Cat-a-log**. Get it? 😸

## Features

- **Concurrent Tailing**: Monitors multiple files simultaneously using goroutines.
- **Dynamic Discovery**: Automatically detects new files matching configured glob patterns during runtime.
- **Log Rotation & Truncation Support**: Handles file rotation (rename/create) and truncation (copytruncate) seamlessly.
- **Filtering**: Exclude specific log lines using regex patterns.
- **Multiline Support**: Aggregates multiline logs (like Java stack traces) into single JSON entries.
- **Enrichment**: Add custom static fields to log entries via configuration.
- **Observability**: Exposes internal metrics in Prometheus format via the `/metrics` endpoint.
- **Graceful Shutdown**: Handles `SIGINT` and `SIGTERM` to ensure all logs are flushed before exiting.
- **Structured Output**: Emits logs as structured JSON (`time`, `host`, `source`, `sourcetype`, `event`), making them easy to ingest into systems like Splunk, Elasticsearch, or Loki.
- **Flexible Output**: Supports both `json` and `raw` (unstructured) output formats.

## Prerequisites

- Go 1.23 or higher

## Installation

1. Initialize the module and download dependencies:
   ```bash
   go mod tidy
   ```

2. Build the application:
   ```bash
   go build -o katalog
   ```

## Configuration

Create a `config.yaml` file in the same directory as the binary.

```yaml
poll_interval: "5s" # How often to check for new files.
# Optional: Output format. Values: "json" (default), "raw"
output_format: "json"
targets:
  - name: "app-logs"
    paths:
      - "/var/log/myapp/*.log"
      - "/tmp/debug.log"
    # Optional: Exclude lines matching this regex
    exclude_pattern: "DEBUG|TRACE"
    # Optional: Handle multiline logs (e.g., stack traces). 
    # The pattern should match the START of a new log entry.
    multiline_pattern: "^\\d{4}-\\d{2}-\\d{2}"
    # Optional: Add static fields to every log entry from this target
    fields:
      env: "production"
      app: "payment-service"
  - name: "system-logs"
    paths:
      - "/var/log/syslog"
```

## Usage

Run the forwarder pointing to your config file:

```bash
./katalog --config config.yaml --metrics-addr :8080
```

The logs will be output to standard output (stdout) in JSON format.

## Containerization

This project uses GoReleaser to create production-ready container images for multiple architectures. The `Containerfile` in the root of the repository is designed to work with the GoReleaser build process.

The image is based on the minimal and secure `registry.access.redhat.com/ubi9/ubi-minimal` base image. It creates a non-root user for added security and copies the pre-built `katalog` binary from the GoReleaser build environment.

### Building with GoReleaser

To build the container images locally, you can use GoReleaser:

```bash
# Run a local build. This will create images like `katalog:latest-amd64`
goreleaser build --snapshot --clean
```

### Manual Build (for testing)

If you want to build the container image manually for a single platform, you can use a multi-stage `Containerfile` like the one below. This is a good way to test changes locally.

1.  **Create a local `Containerfile`:**

    ```dockerfile
    # syntax=docker/dockerfile:1

    # --- Build Stage ---
    FROM golang:1.23 AS builder
    WORKDIR /app

    # Copy sources and download dependencies
    COPY go.mod go.sum ./
    RUN go mod download
    COPY . .

    # Build the static binary
    ARG TARGETPLATFORM=linux/amd64
    RUN CGO_ENABLED=0 GOOS=$(echo $TARGETPLATFORM | cut -d/ -f1) GOARCH=$(echo $TARGETPLATFORM | cut -d/ -f2) go build -o /katalog .

    # --- Run Stage ---
    FROM registry.access.redhat.com/ubi9/ubi-minimal

    # Install runtime requirements
    RUN microdnf install -y ca-certificates tzdata && microdnf clean all

    # Create non-root user
    RUN useradd --uid 10001 --create-home --shell /sbin/nologin katalog
    USER 10001

    # Copy the pre-built binary from the build stage
    COPY --from=builder /katalog /usr/local/bin/katalog

    ENTRYPOINT ["/usr/local/bin/katalog"]
    ```

2.  **Build the image:**
    ```bash
    podman build -t katalog:local .
    ```

3.  **Run the container:**
    This example mounts a local `config.yaml` and the host's `/var/log` directory into the container.

    ```bash
    podman run -d \
      --name katalog \
      -v $(pwd)/config.yaml:/app/config.yaml:Z \
      -v /var/log:/var/log:ro \
      katalog:local --config /app/config.yaml
    ```


