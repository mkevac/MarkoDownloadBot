.PHONY: all push run stop build run-local run-api stop-api debug help

# New default target that prints help information
help:
	@echo "Available targets:"
	@echo "  docker     - Build Docker image locally"
	@echo "  push       - Build and push Docker image for multiple platforms"
	@echo "  run        - Start services using docker-compose"
	@echo "  stop       - Stop docker-compose services"
	@echo "  build      - Build the Go binary"
	@echo "  debug      - Start API service and run locally"
	@echo "Use 'make <target>' to execute a specific target."

# Set the default target to help
.DEFAULT_GOAL := help

docker:
	docker buildx build -t mkevac/markodownloadbot --load .

push:
	docker buildx build --platform linux/amd64,linux/arm64 -t mkevac/markodownloadbot --push .

run:
	docker-compose up -d

stop:
	docker-compose down

build:
	CGO_ENABLED=0 go build -o markodownloadbot .

run-local: build
	IS_LOCAL=true ./markodownloadbot

run-api:
	docker-compose up telegram-bot-api -d

stop-api:
	docker-compose down

debug: run-api run-local
