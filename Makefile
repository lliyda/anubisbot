.PHONY: run build tidy

run:
	go run ./cmd/anubisbot

build:
	go build -o anubisbot.exe ./cmd/anubisbot

tidy:
	go mod tidy