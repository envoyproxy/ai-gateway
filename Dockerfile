FROM gcr.io/distroless/static-debian11:nonroot
ARG COMMAND_NAME
ARG TARGETOS
ARG TARGETARCH

COPY ./out/${COMMAND_NAME}-${TARGETOS}-${TARGETARCH} /${COMMAND_NAME}

USER nonroot:nonroot
ENTRYPOINT ["/${COMMAND_NAME}"]
