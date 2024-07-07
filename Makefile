all:
	GOOS=linux GOARCH=amd64 go build
	docker build -t markodownloadbot .

run:
	docker-compose up -d

stop:
	docker-compose down