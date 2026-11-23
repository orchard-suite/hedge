PREFIX  ?= $(HOME)/.local
VERSION := $(shell cat VERSION)
LDFLAGS := -s -w -X github.com/orchard-suite/hedge/internal/cli.Version=$(VERSION)

.PHONY: install build test check bench land

# The default: build hedge, install it, and set its git hooks.
install: build
	mkdir -p "$(PREFIX)/bin"
	install -m 0755 dist/hedge "$(PREFIX)/bin/hedge"
	"$(PREFIX)/bin/hedge" install

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o dist/hedge ./cmd/hedge

test:
	go test ./...

check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run gofmt -w ." >&2; exit 1; }
	go vet ./...
	go test ./...

bench:
	go test -run x -bench . -benchmem ./internal/match

land: check
	release-damnit land $(BRANCH)
