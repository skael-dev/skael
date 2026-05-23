.PHONY: build test dev

build:
	CGO_ENABLED=0 go build -o bin/skael-server ./cmd/server

test:
	go test ./... -v

dev:
	go run ./cmd/server
