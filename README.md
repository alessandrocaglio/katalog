# Katalog

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

- Go 1.21 or higher

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

To run the log forwarder in a container, you can use the following `Containerfile`.

1. Create a `Containerfile` in the project root:

   ```dockerfile
   # Build Stage
   FROM golang:1.21 AS builder
   WORKDIR /app
   COPY go.mod go.sum ./
   RUN go mod download
   COPY . .
   RUN CGO_ENABLED=0 GOOS=linux go build -o katalog .

   # Run Stage
   FROM registry.access.redhat.com/ubi9/ubi-minimal
   WORKDIR /app
   COPY --from=builder /app/katalog .
   # Copy default config (can be overridden at runtime)
   COPY config.yaml .
   CMD ["./katalog"]
   ```

2. Build the image:
   ```bash
   podman build -t katalog .
   ```

3. Run the container:
   ```bash
   podman run -d \
     --name katalog \
     -v $(pwd)/config.yaml:/app/config.yaml:Z \
     -v /var/log:/var/log:ro \
     katalog
   ```

## Kubernetes Deployment

You can deploy Katalog in Kubernetes as either a `DaemonSet` or a `Deployment`, depending on your use case.

### As a DaemonSet (for Node Logs)

A `DaemonSet` is the ideal pattern when you want to collect logs from every node in your cluster (e.g., system logs from `/var/log`). It ensures that a single `katalog` pod runs on each node.

1.  **Create a ConfigMap manifest (`katalog-configmap.yaml`):**
   ```yaml
   apiVersion: v1
   kind: ConfigMap
   metadata:
     name: katalog-config
   data:
     config.yaml: |
       poll_interval: "5s"
        output_format: "json"
       targets:
         - name: "node-logs"
           paths:
             - "/var/log/*.log"
             - "/var/log/messages"
             - "/var/log/syslog"
   ```

2.  **Create a DaemonSet manifest (`katalog-daemonset.yaml`):**
   ```yaml
   apiVersion: apps/v1
   kind: DaemonSet
   metadata:
     name: katalog
     labels:
       app: katalog
   spec:
     selector:
       matchLabels:
         app: katalog
     template:
       metadata:
         labels:
           app: katalog
       spec:
         # Tolerate running on control-plane nodes
         tolerations:
         - key: node-role.kubernetes.io/control-plane
           operator: Exists
           effect: NoSchedule
         - key: node-role.kubernetes.io/master
           operator: Exists
           effect: NoSchedule
         containers:
           - name: katalog
             image: your-repo/katalog:latest
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
               name: katalog-config
           - name: logs
             hostPath:
               path: /var/log
   ```

3.  **Apply the manifests:**
   ```bash
   kubectl apply -f katalog-configmap.yaml
   kubectl apply -f katalog-daemonset.yaml
   ```

### As a Deployment

A `Deployment` is suitable when you want to collect logs from a specific application, rather than from every node. A common pattern is to run Katalog as a **sidecar container** in your application's pod.

In this scenario, you share a volume between your application container and the `katalog` sidecar. The application writes logs to the volume, and `katalog` reads them.

1.  **Create a Deployment manifest (`app-deployment.yaml`):**
    This example shows an application pod with a `katalog` sidecar.

    ```yaml
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: my-application
    spec:
      replicas: 2
      selector:
        matchLabels:
          app: my-application
      template:
        metadata:
          labels:
            app: my-application
        spec:
          volumes:
          # 1. Shared volume for logs
          - name: app-logs
            emptyDir: {}
          # 2. Volume for the katalog config
          - name: config
            configMap:
              # Assumes a ConfigMap named 'katalog-config' exists
              name: katalog-config
          containers:
          # Your application container
          - name: my-application-container
            image: my-app:1.0
            volumeMounts:
            - name: app-logs
              mountPath: /var/log/app # Writes logs here
          # The Katalog sidecar container
          - name: katalog-sidecar
            image: your-repo/katalog:latest
            imagePullPolicy: IfNotPresent
            volumeMounts:
            - name: config
              mountPath: /app/config.yaml
              subPath: config.yaml
            - name: app-logs
              mountPath: /var/log/app # Reads logs from here
              readOnly: true
    ```

2.  **Apply the manifest:**
    ```bash
    # Make sure you have created the ConfigMap first
    kubectl apply -f app-deployment.yaml
    ```
