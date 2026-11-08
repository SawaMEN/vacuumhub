.PHONY: test vet build

test:
	CGO_ENABLED=1 go test -race ./...

vet:
	CGO_ENABLED=1 go vet ./...

build:
	CGO_ENABLED=1 go build -o bin/vacuumhub ./cmd/vacuumhub
