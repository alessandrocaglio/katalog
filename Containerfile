# syntax=docker/dockerfile:1

FROM registry.access.redhat.com/ubi9/ubi-minimal

# Install runtime requirements only
RUN microdnf install -y \
      ca-certificates \
      tzdata \
    && microdnf clean all

# Create non-root user (OpenShift friendly)
RUN useradd --uid 10001 --create-home --shell /sbin/nologin katalog

LABEL org.opencontainers.image.source="https://github.com/alessandrocaglio/katalog" \
      org.opencontainers.image.description="Lightweight concurrent log tailing agent" \
      org.opencontainers.image.licenses="MIT"

USER 10001

# GoReleaser will place the binary in the build context
COPY katalog /usr/local/bin/katalog

ENTRYPOINT ["/usr/local/bin/katalog"]
