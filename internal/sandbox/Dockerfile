# The default sandbox image: a general-purpose dev environment plus the
# Claude Code CLI. `beethoven serve` builds it on first start if missing.
FROM node:24-bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      git ca-certificates curl jq ripgrep procps less unzip openssh-client \
      build-essential python3 python3-pip python3-venv \
 && rm -rf /var/lib/apt/lists/*

RUN npm install -g @anthropic-ai/claude-code \
 && npm cache clean --force

RUN useradd -m -u 1001 -s /bin/bash agent \
 && mkdir -p /workspace && chown agent:agent /workspace

USER agent
WORKDIR /workspace
ENV PATH=/home/agent/.local/bin:$PATH \
    PIP_BREAK_SYSTEM_PACKAGES=1
