FROM golang:1.24.0-alpine AS deps

WORKDIR /app

COPY go.mod ./
# COPY go.sum ./

# RUN go mod download && go mod verify

FROM golang:1.24.0-alpine AS builder

WORKDIR /app

COPY --from=deps /app/go.mod ./
# COPY --from=deps /app/go.sum ./
COPY main.go ./
COPY internal/ ./internal/

RUN CGO_ENABLED=0 GOOS=linux go build -o riftrelay ./main.go

FROM gcr.io/distroless/base-debian12 AS runner

COPY --from=builder /app/riftrelay /riftrelay

CMD ["/riftrelay"]
