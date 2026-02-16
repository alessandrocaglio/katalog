# syntax=docker/dockerfile:1

FROM registry.access.redhat.com/ubi9/ubi-minimal

# 1. Declare the build-arg that GoReleaser/Buildx provides automatically
ARG TARGETPLATFORM

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

# 2. FIX: GoReleaser v2 places the binary in a platform-specific subfolder
# This variable resolves to things like "linux/amd64/katalog"
COPY $TARGETPLATFORM/katalog /usr/local/bin/katalog

ENTRYPOINT ["/usr/local/bin/katalog"]