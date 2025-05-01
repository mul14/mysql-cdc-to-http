# 1. Build stage
FROM golang:1.24-alpine AS builder

# Accept build arguments for cross-compilation
ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0 \
    GO111MODULE=on \
    GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH}

WORKDIR /app

# Copy Go module files first (enables caching of dependencies)
COPY go.mod go.sum ./
RUN go mod download

# Copy the full source code
COPY . .

# Build the statically linked binary
RUN go build -o /bin/mysql-cdc-to-http .

# 2. Final minimal image
FROM alpine:latest

# Add a non-root user for security
RUN addgroup -S app && adduser -S app -G app

# Copy the binary from the builder stage
COPY --from=builder /bin/mysql-cdc-to-http /usr/local/bin/mysql-cdc-to-http

# Set working directory
WORKDIR /app

# Switch to non-root user
USER app

# Set entrypoint to run the CDC app
ENTRYPOINT ["/usr/local/bin/mysql-cdc-to-http"]
