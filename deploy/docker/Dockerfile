# syslogq server image. Build context: backend/ (the Go module).
#   docker build -f deploy/docker/Dockerfile backend/
#
# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/syslogq ./cmd/syslogq

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/syslogq /syslogq
# API (8080), syslog UDP/TCP (5140; map 514→5140 for privileged access)
EXPOSE 8080 5140/udp 5140
USER nonroot:nonroot
ENTRYPOINT ["/syslogq"]
