# syntax=docker/dockerfile:1

# ---- Build stage ----
FROM golang:1.24-alpine AS builder
WORKDIR /src

# Use the toolchain shipped in the image; never download one over the network
# (go.mod pins toolchain go1.24.4, which golang:1.24-alpine satisfies).
ENV GOTOOLCHAIN=local

# Modules change rarely — download them before copying source so this layer
# is cached across code-only rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/homepage ./cmd/homepage

# /data is prepared here so it can be copied into the final image already
# owned by the nonroot user (uid/gid 65532). Docker initializes fresh named
# volumes from the image's directory contents and ownership, so a mounted
# volume at /data starts out writable by nonroot.
RUN mkdir -p /data && chown 65532:65532 /data

# ---- Runtime stage ----
# distroless/static ships CA certificates (needed for HTTPS favicon fetches)
# and nothing else — no shell, no package manager, no curl.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/homepage /homepage
COPY --from=builder --chown=65532:65532 /data /data

ENV HOMEPAGE_ADDR=:8080 \
    HOMEPAGE_DATA_DIR=/data

VOLUME /data
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/homepage"]

# No HEALTHCHECK: the distroless image has no shell or curl to probe with,
# and the binary has no healthcheck subcommand. Monitor GET /api/health
# from outside the container instead.
