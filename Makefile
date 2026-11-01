.DEFAULT_GOAL := check
.PHONY: build test fmt fmt-check vet lint check mod-check clean

export CGO_ENABLED := 0

# Unique per build, so a rebuilt binary replaces a running loci serve.
VERSION ?= $(shell git describe --always --dirty 2>/dev/null || echo dev)+$(shell date -u +%Y%m%dT%H%M%S)
LDFLAGS := -X github.com/OrestisStefanou/Loci/internal/version.Version=$(VERSION)

build: ## Build bin/loci (static, no cgo)
	go build -ldflags "$(LDFLAGS)" -o bin/loci ./cmd/loci

# tursogo extracts its native library into a cache directory on first use,
# writing the file in place. On an empty cache (a fresh clone or CI runner)
# test binaries running in parallel can read it half written and panic with
# "hash sum mismatch", so one test opens a store first to fill the cache.
test: ## Run all tests with the race detector
	CGO_ENABLED=1 go test -race -count=1 -run '^TestOpenIsIdempotent$$' ./internal/store >/dev/null
	CGO_ENABLED=1 go test -race ./...

fmt: ## Format all Go files
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

lint: ## golangci-lint; skipped with a notice when not installed
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; \
	else echo "golangci-lint not installed, skipping (brew install golangci-lint)"; fi

check: fmt-check vet lint test ## Everything CI runs. Must pass before work is done

# The loci-pick mod (internal/capture/mod) is TypeScript the claude binary
# validates and tests, so it is checked apart from check and CI.
mod-check: ## Validate and test the loci-pick mod; needs claude
	claude plugin validate internal/capture/mod
	claude plugin test internal/capture/mod

clean:
	rm -rf bin
