# Build stage
FROM golang:1.24.0-alpine AS builder

# Install build dependencies for go-sqlite3 (CGO)
RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod tidy

# Copy source code
COPY . .

# Build the application
# CGO_ENABLED=1 is required for go-sqlite3
RUN CGO_ENABLED=1 GOOS=linux go build -o burnout-app main.go

# Final stage
FROM alpine:latest

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache sqlite-libs

# Copy the binary from builder
COPY --from=builder /app/burnout-app .

# Copy templates directory
COPY --from=builder /app/templates ./templates

# Expose the port
EXPOSE 8081

# Command to run
CMD ["./burnout-app"]
