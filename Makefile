# Makefile builds Super-Cache server and CLI binaries.
#
# Architected and Developed By:- Faisal Hanif | imfanee@gmail.com

BINARY_SERVER = supercache
BINARY_CLI = supercache-cli
BUILD_DIR = ./build
LDFLAGS = -ldflags "-s -w -X main.Version=$(shell git describe --tags --always 2>/dev/null || echo dev)"

.PHONY: all build build-server build-cli test test-race bench lint clean install

all: build

build: build-server build-cli

build-server:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -a $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_SERVER) ./cmd/supercache/...

build-cli:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -a $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_CLI) ./cmd/supercache-cli/...

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

bench:
	go test -bench=. -benchmem ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BUILD_DIR)

install: build
	cp $(BUILD_DIR)/$(BINARY_SERVER) /usr/local/bin/
	cp $(BUILD_DIR)/$(BINARY_CLI) /usr/local/bin/
