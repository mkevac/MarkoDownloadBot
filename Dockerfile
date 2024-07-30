# Stage 1: Build the Go binary
FROM golang:latest as builder

# Set the working directory
WORKDIR /app

# Copy the Go source code
COPY . .

# Build the binary for different platforms
ARG TARGETOS
ARG TARGETARCH
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /app/markodownloadbot .

# Stage 2: Create the final image
FROM alpine:latest

RUN apk add --no-cache yt-dlp

# Set the working directory inside the container.
WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/markodownloadbot /app/markodownloadbot

# Give execution permissions to the binary if needed.
RUN chmod +x /app/markodownloadbot

# Define the entrypoint to run the binary.
ENTRYPOINT ["/app/markodownloadbot"]