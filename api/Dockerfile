# syntax=docker/dockerfile:1

# --- Build stage: compile a static binary ---
FROM golang:1.25-alpine AS build
WORKDIR /src

# Download modules first so this layer is cached until go.mod/go.sum change.
# The glob keeps the copy working before go.sum exists (no dependencies yet).
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# --- Runtime stage: small image, non-root user ---
# Alpine (rather than distroless) keeps a shell and busybox wget around for
# debugging and for the compose healthcheck.
FROM alpine:3.22
RUN adduser -D -H -u 10001 app
USER app
COPY --from=build /out/api /usr/local/bin/api

EXPOSE 8080
# Exec form so the binary is PID 1 and receives SIGTERM from `docker stop`.
ENTRYPOINT ["/usr/local/bin/api"]
