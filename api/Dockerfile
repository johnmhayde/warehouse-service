# syntax=docker/dockerfile:1

# One Dockerfile, two images:
#   docker build --target api .      the HTTP API (the default target)
#   docker build --target migrate .  the one-shot migration runner

# --- Build stage: compile static binaries ---
FROM golang:1.26-alpine AS build
WORKDIR /src

# Download modules first so this layer is cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/api ./cmd/migrate

# --- Shared runtime base: small image, non-root user ---
# Alpine (rather than distroless) keeps a shell and busybox wget around for
# debugging.
FROM alpine:3.22 AS runtime
RUN adduser -D -H -u 10001 app
USER app

# --- Migration runner ---
# Migrations are embedded in the binary, so the image needs nothing else.
FROM runtime AS migrate
COPY --from=build /out/migrate /usr/local/bin/migrate
ENTRYPOINT ["/usr/local/bin/migrate"]
CMD ["status"]

# --- API (last, so it is the default target) ---
FROM runtime AS api
COPY --from=build /out/api /usr/local/bin/api
EXPOSE 8080
# Exec form so the binary is PID 1 and receives SIGTERM from `docker stop`.
ENTRYPOINT ["/usr/local/bin/api"]
