VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w \
  -X github.com/kervanserver/kervan/internal/build.Version=$(VERSION) \
  -X github.com/kervanserver/kervan/internal/build.Commit=$(COMMIT) \
  -X github.com/kervanserver/kervan/internal/build.Date=$(DATE)

.PHONY: build test clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/kervan ./cmd/kervan

test:
	go test ./...

clean:
	rm -rf bin

