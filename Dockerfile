# Multi-Stage Production Dockerfile for Boron Labs M-Tap Switching Engine

# --- Stage 1: Build Stage ---
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Cache Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy source tree
COPY . .

# Compile optimized static binaries
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /bin/m-tap-server ./cmd/server/main.go
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /bin/m-tap-seed ./cmd/seed/main.go

# --- Stage 2: Final Minimal Runtime ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata curl

# Create non-root system user
RUN addgroup -S mtap && adduser -S mtap -G mtap

WORKDIR /app

COPY --from=builder /bin/m-tap-server /app/m-tap-server
COPY --from=builder /bin/m-tap-seed /app/m-tap-seed

USER mtap:mtap

EXPOSE 8080

ENTRYPOINT ["/app/m-tap-server"]

