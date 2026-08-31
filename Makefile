.PHONY: build test lint run record-fixtures

build:
	go build -o bin/uaanime ./cmd/uaanime

test:
	go test ./...

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...
	golangci-lint run

run: build
	./bin/uaanime

record-fixtures:
	go run ./tools/record
