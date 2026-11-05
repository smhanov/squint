GO ?= go

.PHONY: build test lint tidy

build:
	$(GO) build ./...

test:
	$(GO) test ./...

lint:
	$(GO) vet ./...
	golangci-lint run

tidy:
	$(GO) mod tidy
