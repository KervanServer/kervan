VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w \
  -X github.com/kervanserver/kervan/internal/build.Version=$(VERSION) \
  -X github.com/kervanserver/kervan/internal/build.Commit=$(COMMIT) \
  -X github.com/kervanserver/kervan/internal/build.Date=$(DATE)

.PHONY: build webui test clean docker-build compose-config compose-up compose-down release-snapshot release-check

build: webui
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/kervan ./cmd/kervan

webui:
	cd webui && npm ci && npm run build
	rm -rf internal/webui/dist
	cp -r webui/dist internal/webui/dist

test:
	go test ./...

clean:
	rm -rf bin

docker-build:
	docker build \
		--build-arg VERSION="$(VERSION)" \
		--build-arg COMMIT="$(COMMIT)" \
		--build-arg DATE="$(DATE)" \
		-t kervan:$(VERSION) .

compose-config:
	docker compose config

compose-up:
	docker compose up -d --build

compose-down:
	docker compose down

release-check:
	goreleaser check

release-snapshot:
	goreleaser release --snapshot --clean
