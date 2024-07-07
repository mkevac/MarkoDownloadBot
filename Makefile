all:
	GOOS=linux GOARCH=amd64 go build
	docker build -t mkevac/markodownloadbot .
	docker push mkevac/markodownloadbot

run:
	docker-compose up -d

stop:
	docker-compose down