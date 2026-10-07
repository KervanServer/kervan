VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w \
  -X github.com/kervanserver/kervan/internal/build.Version=$(VERSION) \
  -X github.com/kervanserver/kervan/internal/build.Commit=$(COMMIT) \
  -X github.com/kervanserver/kervan/internal/build.Date=$(DATE)

.PHONY: build webui test check audit clean docker-build compose-config compose-up compose-down release-snapshot release-check

build: webui
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/kervan ./cmd/kervan

webui:
	go run ./scripts

test:
	go test ./...

# check runs locally what the (manual-only) GitHub Actions CI runs:
# gofmt, vet, staticcheck, tests, race tests, WebUI tests and the embedded
# dist drift check. Use it instead of pushing to trigger CI.
check:
	@unformatted="$$(gofmt -l $$(git ls-files '*.go'))"; \
	  if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; \
	  else echo "staticcheck not installed: go install honnef.co/go/tools/cmd/staticcheck@latest"; exit 1; fi
	go test ./... -count=1
	CGO_ENABLED=1 go test -race ./... -count=1
	cd webui && npm test
	go run ./scripts
	git diff --exit-code -- internal/webui/dist

# audit scans Go code (reachable stdlib/module vulnerabilities) and the WebUI
# dependency tree for known security advisories.
audit:
	@if command -v govulncheck >/dev/null 2>&1; then govulncheck ./...; \
	  else echo "govulncheck not installed: go install golang.org/x/vuln/cmd/govulncheck@latest"; exit 1; fi
	cd webui && npm audit

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
