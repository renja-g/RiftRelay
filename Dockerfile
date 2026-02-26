# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26.0

FROM golang:${GO_VERSION}-alpine AS builder
WORKDIR /src

ARG TARGETOS=linux
ARG TARGETARCH

COPY go.mod ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
	go build -trimpath -ldflags="-s -w -buildid=" -o /out/riftrelay .

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata wget
RUN addgroup -S app && adduser -S -G app -h /nonexistent -s /sbin/nologin app

WORKDIR /app
COPY --from=builder /out/riftrelay /usr/local/bin/riftrelay

USER app:app

ENV PORT=8985
EXPOSE 8985

HEALTHCHECK --interval=15s --timeout=3s --start-period=20s --retries=3 \
	CMD wget --spider -q "http://127.0.0.1:${PORT:-8985}/healthz" || exit 1

ENTRYPOINT ["/usr/local/bin/riftrelay"]
