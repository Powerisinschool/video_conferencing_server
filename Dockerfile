# Stage 1: Build
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy dependency files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
# We disable CGO for a purely static binary (easier for Alpine)
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -o video_server ./cmd/server/main.go

# Stage 2: Run
FROM alpine:latest

WORKDIR /root/

# Copy the binary from builder
COPY --from=builder /app/video_server .

# CRITICAL: Copy the static frontend files
COPY --from=builder /app/static ./static

# Expose the port (Documentation only, Render ignores this)
EXPOSE 8080

# Command to run
CMD ["./video_server"]
