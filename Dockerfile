FROM golang:1.27-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
# CGO off keeps the binary static: notmuch is executed, never linked.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/your-mail-mcp .

FROM debian:trixie-slim
LABEL io.modelcontextprotocol.server.name="io.github.wildsurfer/your-mail-mcp"
RUN apt-get update \
 && apt-get install -y --no-install-recommends isync notmuch w3m ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/your-mail-mcp /usr/local/bin/your-mail-mcp
ENV MAILDIR=/mail INDEX=/index CONFIG=/config/accounts.json LISTEN_ADDR=:8080
# Volumes are created root-owned on first mount; pre-create and chown so the
# non-root user below can actually write into them.
RUN mkdir -p /mail /index && chown 1000:1000 /mail /index
VOLUME ["/mail", "/index"]
EXPOSE 8080
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/your-mail-mcp"]
CMD ["stdio"]
