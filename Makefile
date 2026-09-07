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

# Cross-platform binaries (no cgo, so plain GOOS/GOARCH builds work).
release:
	mkdir -p dist
	GOOS=linux  GOARCH=amd64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/eagent-linux-amd64 ./cmd/eagent
	GOOS=linux  GOARCH=arm64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/eagent-linux-arm64 ./cmd/eagent
	GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/eagent-darwin-arm64 ./cmd/eagent
	GOOS=darwin GOARCH=amd64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/eagent-darwin-amd64 ./cmd/eagent
	cd dist && sha256sum eagent-* > SHA256SUMS
