# syntax=docker/dockerfile:1.7

# ---- Build stage ----
FROM golang:1.26-alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src

# Keep dependency layers cacheable while excluding runtime configuration from the image.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/env-vault ./cmd

# ---- Runtime stage ----
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 envvault \
    && adduser -S -D -H -u 10001 -G envvault envvault

WORKDIR /app

COPY --from=builder --chown=10001:10001 /out/env-vault /app/env-vault

# Runtime configuration must be mounted at /app/configs/config.yaml or provided through env overrides.
# Do not copy repository configuration here: it may contain local credentials.
ENV TZ=Asia/Shanghai

USER 10001:10001
EXPOSE 8090

ENTRYPOINT ["/app/env-vault"]
