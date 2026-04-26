.PHONY: build test e2e clean

BINARY := pgmint
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X github.com/studiodwyer/pgmint/cmd.Version=$(VERSION)"

build:
	go build $(LDFLAGS) -o dist/$(BINARY) .

test:
	go test ./...

e2e: build
	./scripts/e2e-test.sh

clean:
	rm -f dist/*
