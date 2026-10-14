# Sandbox image with a fake `claude`, for tests and UI development:
#   docker build -t beethoven-fake:latest internal/session/testdata/fakeagent
#   BEETHOVEN_IMAGE=beethoven-fake:latest beethoven serve
FROM alpine:3.20
RUN apk add --no-cache git jq \
 && adduser -D -u 1001 agent \
 && mkdir -p /workspace && chown agent:agent /workspace
COPY --chmod=755 claude /usr/local/bin/claude
USER agent
WORKDIR /workspace
