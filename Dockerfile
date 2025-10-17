# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

# Variant to use as the base image. By default 'static' is used, but the 'aigw' image
# needs to use 'base-nossl' because it needs 'glibc' for running Envoy.
ARG VARIANT=static
ARG COMMAND_NAME

# Pre-download Envoy for aigw using func-e. This reduces latency and avoids
# needing to declare a volume for the Envoy binary, which is tricky in Docker
# Compose v2 because volumes end up owned by root.
FROM golang:1.25 AS envoy-downloader
ARG TARGETOS
ARG TARGETARCH
ARG COMMAND_NAME
# Download Envoy binary to AIGW_DATA_HOME for the nonroot user
WORKDIR /build
RUN if [ "$COMMAND_NAME" = "aigw" ]; then \
      go install github.com/tetratelabs/func-e/cmd/func-e@latest && \
      FUNC_E_DATA_HOME=/home/nonroot/.local/share/aigw func-e --platform ${TARGETOS}/${TARGETARCH} run --version; \
    fi \
    # Create directories for the nonroot user
    && mkdir -p /home/nonroot /tmp/envoy-gateway/certs \
    && chown -R 65532:65532 /home/nonroot /tmp/envoy-gateway \
    && chmod -R 755 /home/nonroot /tmp/envoy-gateway

FROM gcr.io/distroless/${VARIANT}-debian12:nonroot
ARG COMMAND_NAME
ARG TARGETOS
ARG TARGETARCH

# Copy pre-downloaded Envoy binary and EG certs directory
COPY --from=envoy-downloader /home/nonroot /home/nonroot
COPY --from=envoy-downloader /tmp/envoy-gateway /tmp/envoy-gateway
COPY ./out/${COMMAND_NAME}-${TARGETOS}-${TARGETARCH} /app

USER nonroot:nonroot

# Set AIGW_RUN_ID=0 for predictable file paths in containers.
# This creates the following directory structure:
#   ~/.config/aigw/                     - XDG config (e.g., envoy-version preference)
#   ~/.local/share/aigw/                - XDG data (downloaded Envoy binaries via func-e)
#   ~/.local/state/aigw/runs/0/         - XDG state (aigw.log, envoy-gateway-config.yaml, extproc-config.yaml, resources/)
#   ~/.local/state/aigw/envoy-runs/0/   - XDG state (func-e stdout.log, stderr.log)
#   /tmp/aigw-0/                        - XDG runtime (uds.sock, admin-address.txt)
ENV AIGW_RUN_ID=0

# The healthcheck subcommand performs an HTTP GET to localhost:1064/healthlthy for "aigw run".
# NOTE: This is only for aigw in practice since this is ignored by Kubernetes.
HEALTHCHECK --interval=10s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/app", "healthcheck"]

ENTRYPOINT ["/app"]

# Default CMD for aigw - uses AIGW_RUN_ID from environment
CMD ["run"]
