.PHONY: build test check install

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -ldflags "-s -w -X main.version=$(VERSION)" -o eagent ./cmd/eagent

test:
	go test -race ./...

check: test
	go vet ./...
	test -z "$$(gofmt -l .)"

install:
	go install ./cmd/eagent
