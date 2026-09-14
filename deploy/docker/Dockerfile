# syslogq server image. Build context: repo root (Go module + frontend).
#   docker build -f deploy/docker/Dockerfile .
#
# syntax=docker/dockerfile:1

FROM node:24-alpine AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build
# Hand off to the Go build below.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /web/dist /src/internal/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/syslogq ./cmd/syslogq

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/syslogq /syslogq
# API + web UI (8080), syslog UDP/TCP (5140; map 514→5140 for privileged access)
EXPOSE 8080 5140/udp 5140
USER nonroot:nonroot
ENTRYPOINT ["/syslogq"]
