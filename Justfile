# MarkoDownloadBot - Task Runner
# Use `just <recipe>` or simply `just` to list all available recipes

# Settings
set dotenv-load
set shell := ["bash", "-uc"]

# Variables
image_name := "mkevac/markodownloadbot"
platforms := "linux/amd64,linux/arm64"
binary_name := "markodownloadbot"
git_tag := `git describe --tags --exact-match 2>/dev/null | sed 's/^v//' || echo "latest"`
has_tag := `git describe --tags --exact-match >/dev/null 2>&1 && echo "true" || echo "false"`

# Default recipe - list all available recipes
default:
    @just --list

# === Build & Test ===

# Build the Go binary
build:
    CGO_ENABLED=0 go build -o {{binary_name}} .

# Run Go tests
test:
    go test -v ./...

# Run golangci-lint
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! command -v golangci-lint &> /dev/null; then
        echo "Installing golangci-lint..."
        go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
    fi
    golangci-lint run ./...

# Remove build artifacts
clean:
    rm -f {{binary_name}}

# === Docker ===

# Build Docker image locally with git tag detection
docker:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "{{has_tag}}" = "true" ]; then
        TAG="{{git_tag}}"
        echo "Building Docker image with tag: $TAG"
        docker buildx build -t {{image_name}}:$TAG -t {{image_name}}:latest --load .
    else
        echo "No Git tag found. Building Docker image with 'latest' tag."
        docker buildx build -t {{image_name}}:latest --load .
    fi

# Build and push Docker image for multiple platforms (requires git tag)
push:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "{{has_tag}}" != "true" ]; then
        echo "ERROR: Cannot push without a git tag on current commit"
        echo "Create a tag first with: just bump"
        echo "Then push the tag, or use: git tag v<version>"
        exit 1
    fi
    TAG="{{git_tag}}"
    echo "Building and pushing Docker image with tag: $TAG"
    docker buildx build --platform {{platforms}} -t {{image_name}}:$TAG -t {{image_name}}:latest --push .

# === Docker Compose ===

# Start services using docker-compose
run:
    docker-compose up -d

# Stop docker-compose services
stop:
    docker-compose down

# Start telegram-bot-api service
run-api:
    docker-compose up telegram-bot-api -d

# Stop all services (alias for stop)
stop-api:
    docker-compose down

# View docker-compose logs (optionally specify service name)
logs service="":
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -n "{{service}}" ]; then
        docker-compose logs -f {{service}}
    else
        docker-compose logs -f
    fi

# === Local Development ===

# Build and run locally with development settings
run-local: build
    IS_LOCAL=true COOKIES_FILE=cookies.txt ./{{binary_name}}

# Build and run locally with no Telegram (HTTP API only)
run-api-only: build
    IS_LOCAL=true COOKIES_FILE=cookies.txt TELEGRAM_BOT_API_TOKEN= ./{{binary_name}}

# Start API service and run bot locally (debug mode)
debug: run-api run-local

# === Versioning ===

# Bump minor version and create new git tag (e.g., v1.6.0 → v1.7.0)
bump:
    #!/usr/bin/env bash
    set -euo pipefail

    # Get the latest tag
    LATEST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")

    if [ -z "$LATEST_TAG" ]; then
        echo "ERROR: No existing tags found"
        echo "Create the first tag manually: git tag v0.1.0"
        exit 1
    fi

    # Remove 'v' prefix and parse version
    VERSION="${LATEST_TAG#v}"

    # Split into major.minor.patch
    IFS='.' read -r MAJOR MINOR PATCH <<< "$VERSION"

    # Validate numeric components
    if ! [[ "$MAJOR" =~ ^[0-9]+$ ]] || ! [[ "$MINOR" =~ ^[0-9]+$ ]]; then
        echo "ERROR: Invalid tag format: $LATEST_TAG"
        echo "Expected format: v<major>.<minor>.<patch>"
        exit 1
    fi

    # Increment minor version, reset patch
    NEW_MINOR=$((MINOR + 1))
    NEW_TAG="v${MAJOR}.${NEW_MINOR}.0"

    # Check if tag already exists
    if git rev-parse "$NEW_TAG" >/dev/null 2>&1; then
        echo "ERROR: Tag $NEW_TAG already exists"
        exit 1
    fi

    # Create the tag
    echo "Creating new tag: $NEW_TAG (from $LATEST_TAG)"
    git tag "$NEW_TAG"
    echo "✓ Tag created successfully"
    echo ""
    echo "Next steps:"
    echo "  1. Push the tag: git push origin $NEW_TAG"
    echo "  2. Build and push: just push"

# === Info ===

# Show git tag and version information
version:
    @echo "Current commit tag: {{git_tag}}"
    @echo "Has tag: {{has_tag}}"
    @echo "Latest tag: $(git describe --tags --abbrev=0 2>/dev/null || echo 'none')"
    @echo "Image name: {{image_name}}"
    @echo "Binary name: {{binary_name}}"
