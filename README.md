# Go Log Forwarder

A lightweight, concurrent log forwarding agent written in Go. It monitors multiple log files defined by glob patterns, enriches the log lines with metadata (timestamp, host, source), and outputs them as JSON to `stdout`.

## Features

- **Concurrent Tailing**: Monitors multiple files simultaneously using goroutines.
- **Dynamic Discovery**: Automatically detects new files matching configured glob patterns during runtime.
- **Log Rotation & Truncation Support**: Handles file rotation (rename/create) and truncation (copytruncate) seamlessly.
- **Filtering**: Exclude specific log lines using regex patterns.
- **Multiline Support**: Aggregates multiline logs (like Java stack traces) into single JSON entries.
- **Enrichment**: Add custom static fields to log entries via configuration.
- **Observability**: Exposes internal metrics in Prometheus format via the `/metrics` endpoint.
- **Graceful Shutdown**: Handles `SIGINT` and `SIGTERM` to ensure all logs are flushed before exiting.
- **Structured Output**: Emits logs in JSON format for easy ingestion by log aggregators.

## Prerequisites

- Go 1.21 or higher

## Installation

1. Initialize the module and download dependencies:
   ```bash
   go mod tidy
   ```

2. Build the application:
   ```bash
   go build -o log-forwarder
   ```

## Configuration

Create a `config.yaml` file in the same directory as the binary.

```yaml
poll_interval: "5s"
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
./log-forwarder --config config.yaml --metrics-addr :8080
```

The logs will be output to standard output (stdout) in JSON format.

## Containerization

To run the log forwarder in a container, you can use the following `Containerfile`.

1. Create a `Containerfile` in the project root:

   ```dockerfile
   # Build Stage
   FROM golang:1.21 AS builder
   WORKDIR /app
   COPY go.mod go.sum ./
   RUN go mod download
   COPY . .
   RUN CGO_ENABLED=0 GOOS=linux go build -o log-forwarder .

   # Run Stage
   FROM registry.access.redhat.com/ubi9/ubi-minimal
   WORKDIR /app
   COPY --from=builder /app/log-forwarder .
   # Copy default config (can be overridden at runtime)
   COPY config.yaml .
   CMD ["./log-forwarder"]
   ```

2. Build the image:
   ```bash
   podman build -t log-forwarder .
   ```

3. Run the container:
   ```bash
   podman run -d \
     --name log-forwarder \
     -v $(pwd)/config.yaml:/app/config.yaml:Z \
     -v /var/log:/var/log:ro \
     log-forwarder
   ```

## Kubernetes Deployment

To deploy the forwarder as a DaemonSet to monitor logs on all nodes:

1. Create a ConfigMap manifest (`configmap.yaml`):
   ```yaml
   apiVersion: v1
   kind: ConfigMap
   metadata:
     name: log-forwarder-config
   data:
     config.yaml: |
       poll_interval: "5s"
       targets:
         - name: "node-logs"
           paths:
             - "/var/log/*.log"
   ```

2. Create a DaemonSet manifest (`daemonset.yaml`):
   ```yaml
   apiVersion: apps/v1
   kind: DaemonSet
   metadata:
     name: log-forwarder
   spec:
     selector:
       matchLabels:
         app: log-forwarder
     template:
       metadata:
         labels:
           app: log-forwarder
       spec:
         containers:
           - name: log-forwarder
             image: log-forwarder:latest
             imagePullPolicy: IfNotPresent
             ports:
               - containerPort: 8080
                 name: metrics
             livenessProbe:
               httpGet:
                 path: /metrics
                 port: metrics
               initialDelaySeconds: 5
               periodSeconds: 15
               failureThreshold: 3
             volumeMounts:
               - name: config
                 mountPath: /app/config.yaml
                 subPath: config.yaml
               - name: logs
                 mountPath: /var/log
                 readOnly: true
         volumes:
           - name: config
             configMap:
               name: log-forwarder-config
           - name: logs
             hostPath:
               path: /var/log
   ```

3. Apply the manifests:
   ```bash
   kubectl apply -f configmap.yaml
   kubectl apply -f daemonset.yaml
   ```

## OpenShift Deployment

Deploying on OpenShift is similar to Kubernetes, but requires handling Security Context Constraints (SCC) if accessing host paths.

1. Create a new project:
   ```bash
   oc new-project log-collection
   ```

2. Grant the `privileged` SCC to the default service account (required for `hostPath` access):
   ```bash
   oc adm policy add-scc-to-user privileged -z default
   ```
   *> **Note**: For production, it is recommended to create a dedicated ServiceAccount.*

3. Apply the configuration and deployment:
   ```bash
   oc apply -f configmap.yaml
   oc apply -f daemonset.yaml
   ```