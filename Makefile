all:
	GOOS=linux GOARCH=amd64 go build
	docker buildx build --platform linux/amd64 -t mkevac/markodownloadbot .

push:
	docker buildx build --platform linux/amd64 -t mkevac/markodownloadbot --push .

run:
	docker-compose up -d

stop:
	docker-compose down