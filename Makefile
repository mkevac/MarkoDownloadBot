all:
	docker buildx build -t mkevac/markodownloadbot .

push:
	docker buildx build --platform linux/amd64,linux/arm64 -t mkevac/markodownloadbot --push .

run:
	docker-compose up -d

stop:
	docker-compose down
