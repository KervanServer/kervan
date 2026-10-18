VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w \
  -X github.com/kervanserver/kervan/internal/build.Version=$(VERSION) \
  -X github.com/kervanserver/kervan/internal/build.Commit=$(COMMIT) \
  -X github.com/kervanserver/kervan/internal/build.Date=$(DATE)

.PHONY: build webui test check check-sqlite check-gitignore audit clean docker-build compose-config compose-up compose-down release-snapshot release-check interop merge-pr ci-sample

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
	./scripts/check-gitignore-safety.sh
	@unformatted="$$(gofmt -l $$(git ls-files '*.go'))"; \
	  if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; \
	  else echo "staticcheck not installed: go install honnef.co/go/tools/cmd/staticcheck@latest"; exit 1; fi
	go test ./... -count=1
	CGO_ENABLED=1 go test -race ./... -count=1
	$(MAKE) check-sqlite
	cd webui && npm test
	go run ./scripts
	git diff --exit-code -- internal/webui/dist

# check-gitignore guards the rules that keep the memory database untracked.
# It runs in `check` and in the Backend Checks job, because no Go tool or
# test suite reads .gitignore: without this, a directory-level negation
# would commit a live SQLite database and every required context would
# still pass.
check-gitignore:
	./scripts/check-gitignore-safety.sh

# check-sqlite runs the suite against the opt-in SQLite backend. The steps
# above only exercise the default JSON backend, and the SQLite engine exists
# precisely to improve concurrency, so its transactions and locking must run
# under -race too rather than only on an ad-hoc local check.
check-sqlite:
	KERVAN_STORE_BACKEND=sqlite go test ./... -count=1
	CGO_ENABLED=1 KERVAN_STORE_BACKEND=sqlite go test -race ./... -count=1

# merge-pr merges a pull request, but only after scripts/merge-pr.sh pre-flights
# the live state with gh. Every required check must have a completed,
# successful check-run AT THE CURRENT HEAD SHA; anything pending or failing
# refuses the merge. The merge body is then generated from that state, so a
# merge message cannot assert a check state nobody verified.
#
#   make merge-pr PR=123          merge only when the gate is satisfied
#   make merge-pr PR=123 ADMIN=1  additionally use the admin override
merge-pr:
	@scripts/merge-pr.sh

# ci-sample dispatches the CI workflow N times and counts only SETTLED
# check-runs. `gh run watch` returning does not mean the check-run read next
# has settled: reading immediately yields conclusion="" while the job is still
# running, which is indistinguishable from a failure. The script therefore polls
# until every check-run at the head reports status=completed, and checks status
# alongside conclusion, so an unsettled job is never counted as green.
#
#   make ci-sample N=10
#   make ci-sample N=10 JOB="Windows Store Tests" REF=master
ci-sample:
	@scripts/ci-sample.sh

# audit scans Go code (reachable stdlib/module vulnerabilities) and the WebUI
# dependency tree for known security advisories.
# interop runs third-party clients (lftp, rclone, paramiko) against a running
# server; see scripts/interop/run.sh for the environment it needs.
interop:
	docker build -q -t kervan-interop scripts/interop
	docker run --rm --network host -e KERVAN_HOST -e FTP_PORT -e FTPS_PORT -e SFTP_PORT \
	  -e KERVAN_USER -e KERVAN_PASSWORD \
	  -v $(CURDIR)/scripts/interop/run.sh:/run.sh:ro kervan-interop /run.sh

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
