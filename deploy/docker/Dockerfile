# syslogq server image. Build context: repo root (Go module + frontend).
#   docker build -f deploy/docker/Dockerfile .
#
# syntax=docker/dockerfile:1

FROM node:24-alpine AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
# vite.config writes into ../backend for go:embed; inside this isolated
# stage keep the output local so the handoff COPY below stays stable.
RUN npm run build -- --outDir dist
# Hand off to the Go build below.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /web/dist /src/internal/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/syslogq ./cmd/syslogq \
    && mkdir -p /out/data && touch /out/data/.keep

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/syslogq /syslogq
# State dir (auth DB) owned by the nonroot runtime user (UID 65532) so the
# server can create its database there without root.
COPY --from=build --chown=65532:65532 /out/data /var/lib/syslogq
# API + web UI (8080), syslog UDP/TCP (5140; map 514→5140 for privileged access)
EXPOSE 8080 5140/udp 5140
USER nonroot:nonroot
ENTRYPOINT ["/syslogq"]
