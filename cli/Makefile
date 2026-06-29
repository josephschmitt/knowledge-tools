.PHONY: all build install run test test-coverage clean lint vet release-check release-local sync-template sync-embed

VERSION := $(shell git describe --tags --abbrev=0 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
BIN := knowledge-tools
PKG := ./cmd/knowledge-tools

all: build

build:
	go build $(LDFLAGS) -o $(BIN) $(PKG)

install:
	go install $(LDFLAGS) $(PKG)

run: build
	./$(BIN)

test:
	go test -cover ./...

test-coverage:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out -o coverage.html

vet:
	go vet ./...

lint: vet
	golangci-lint run

# GoReleaser targets
release-check:
	goreleaser check

release-local:
	goreleaser release --snapshot --clean --skip=publish

# Sync the embedded assets from their repo-root sources of truth (the CLI embeds committed copies
# because the binary is standalone; CI guards against drift).
sync-embed: sync-template

sync-template:
	rm -rf internal/initvault/template
	cp -R ../template internal/initvault/template

clean:
	rm -f $(BIN)
	rm -rf dist/
