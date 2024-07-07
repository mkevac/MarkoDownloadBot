# Use a minimal base image. Choose the base image according to your binary's requirements.
FROM alpine:latest

RUN apk add --no-cache yt-dlp

# Set the working directory inside the container.
WORKDIR /app

# Copy the binary into the image.
COPY markodownloadbot /app/markodownloadbot

# Give execution permissions to the binary if needed.
RUN chmod +x /app/markodownloadbot

# Define the entrypoint to run the binary.
ENTRYPOINT ["/app/markodownloadbot"]