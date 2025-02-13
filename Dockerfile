# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

FROM gcr.io/distroless/static-debian11:nonroot
ARG COMMAND_NAME
ARG TARGETOS
ARG TARGETARCH

COPY ./out/${COMMAND_NAME}-${TARGETOS}-${TARGETARCH} /app

USER nonroot:nonroot
ENTRYPOINT ["/app"]
