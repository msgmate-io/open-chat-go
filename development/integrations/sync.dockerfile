# syntax=docker/dockerfile:1
# Materializes integration checkouts and generates the Go workspace for the
# selected profile. Runs as a one-shot init service before backend/frontend.
FROM python:3.12-alpine

RUN apk add --no-cache git \
    && pip install --no-cache-dir pyyaml

WORKDIR /workspace
ENV PYTHONPATH=/workspace/development/integrations

ENTRYPOINT ["python3", "-m", "openchat_integrations"]
CMD ["prepare"]
