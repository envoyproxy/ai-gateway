# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

# Variant to use as the base image. By default 'static' is used, but the 'aigw' image
# needs to use 'base-nossl' because it needs 'glibc' to use func-e to pull the Envoy binaries.
ARG VARIANT

FROM gcr.io/distroless/${VARIANT}-debian12:nonroot
ARG COMMAND_NAME
ARG TARGETOS
ARG TARGETARCH

COPY ./out/${COMMAND_NAME}-${TARGETOS}-${TARGETARCH} /app

USER nonroot:nonroot
ENTRYPOINT ["/app"]
