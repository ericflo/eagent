.PHONY: build test check install

build:
	go build -ldflags "-s -w" -o eagent ./cmd/eagent

test:
	go test -race ./...

check: test
	go vet ./...
	test -z "$$(gofmt -l .)"

install:
	go install ./cmd/eagent
