.PHONY: test build run

test:
	CGO_ENABLED=1 go test ./...

build:
	CGO_ENABLED=1 go build -o bin/vacuumhub ./cmd/vacuumhub

run:
	CGO_ENABLED=1 go run ./cmd/vacuumhub
