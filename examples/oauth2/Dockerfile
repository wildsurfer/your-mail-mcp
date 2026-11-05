# The your-mail-mcp image plus python3, for an account whose pass_cmd runs
# mutt's mutt_oauth2.py token helper. The helper itself is not copied in: it
# is mounted with its token file under /oauth. See README.md here.
ARG BASE=ghcr.io/wildsurfer/your-mail-mcp:latest
FROM ${BASE}
USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends python3 \
 && rm -rf /var/lib/apt/lists/*
# The helper rewrites its token file on every refresh, so /oauth is mounted
# writable; pre-create and chown it as the base image does for /mail and
# /index.
RUN mkdir -p /oauth && chown 1000:1000 /oauth
USER 1000:1000
