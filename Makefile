.PHONY: build test lint run record-fixtures record-fixtures-new

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

record-fixtures-new:
	go run ./tools/record -new
