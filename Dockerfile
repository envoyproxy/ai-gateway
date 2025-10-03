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
# Hard-coded directory for envoy-gateway resources
# See https://github.com/envoyproxy/gateway/blob/d95ce4ce564cfff47ed1fd6c97e29c1058aa4a61/internal/infrastructure/host/proxy_infra.go#L16
WORKDIR /tmp/envoy-gateway
RUN if [ "$COMMAND_NAME" = "aigw" ]; then \
      go install github.com/tetratelabs/func-e/cmd/func-e@latest && \
      func-e --platform ${TARGETOS}/${TARGETARCH} --home-dir . run --version; \
    fi \
    && mkdir -p certs \
    && chown -R 65532:65532 . \
    && chmod -R 755 .

FROM gcr.io/distroless/${VARIANT}-debian12:nonroot
ARG COMMAND_NAME
ARG TARGETOS
ARG TARGETARCH

COPY --from=envoy-downloader /tmp/envoy-gateway /tmp/envoy-gateway
COPY ./out/${COMMAND_NAME}-${TARGETOS}-${TARGETARCH} /app

USER nonroot:nonroot

# The healthcheck subcommand performs an HTTP GET to localhost:1064/healthlthy for "aigw run".
# NOTE: This is only for aigw in practice since this is ignored by Kubernetes.
HEALTHCHECK --interval=10s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/app", "healthcheck"]

ENTRYPOINT ["/app"]
