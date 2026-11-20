# Use the official Go image as the build environment
FROM golang:1.24 AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy go.mod and go.sum to download dependencies first
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the binary from the cmd/simplemon-agent directory
RUN CGO_ENABLED=0 GOOS=linux go build -v -o myapp ./cmd/simplemon-agent
# // Build the binary from the specified main package directory

# Create a smaller final image
FROM alpine:3.17
WORKDIR /app

# Install CA certificates (if needed for HTTPS connections)
RUN apk add --no-cache ca-certificates
# // Install certificates

# Copy the binary from the builder stage
COPY --from=builder /app/myapp .

# Run the compiled binary
CMD ["./myapp"]
# // Start the application
