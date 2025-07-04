# Use pinned Alpine Linux base image for small, reproducible size
FROM alpine:3.20

LABEL maintainer="dev@opencode.ai"
LABEL description="Development/testing sandbox with opencode, GitHub CLI, and common tools"

# Install system dependencies and common development tools
RUN apk update && apk add --no-cache \
    bash \
    bash-completion \
    vim \
    git \
    github-cli \
    curl \
    wget \
    ca-certificates \
    tar \
    gzip \
    jq \
    less \
    make \
    gcc \
    musl-dev \
    python3 \
    py3-pip \
    which \
    util-linux \
    docker \
    docker-compose

# Install official opencode CLI
RUN curl -fsSL https://opencode.ai/install | bash

# Set working directory for project code
WORKDIR /workspace

# Set default shell to bash
SHELL ["/bin/bash"]

# Add opencode to PATH
ENV PATH="/root/.opencode/bin:$PATH"

# Docker-in-Docker entrypoint (starts nested dockerd)
COPY --chmod=0755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]

# Default startup command
CMD ["bash"]