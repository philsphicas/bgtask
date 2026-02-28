BINARY  := bgtask
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOFLAGS := -trimpath
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
CGO     := $(shell go env CGO_ENABLED)
RACE    := $(if $(filter 1,$(CGO)),-race,)

.PHONY: build install clean test cover cover-e2e lint vulncheck fmt fmt-check help

.DEFAULT_GOAL := help

build: ## Build the bgtask binary
	go build $(GOFLAGS) $(LDFLAGS) -o bin/$(BINARY) ./cmd/bgtask

install: ## Install to $GOPATH/bin
	go install $(GOFLAGS) $(LDFLAGS) ./cmd/bgtask

test: ## Run tests (with -race if CGO is available)
ifneq ($(RACE),)
	go test -race -count=1 ./...
else
	@echo "warning: CGO disabled, running tests without -race"
	go test -count=1 ./...
endif

cover: ## Run tests with coverage report
ifneq ($(RACE),)
	go test -race -coverprofile=coverage.txt ./...
else
	@echo "warning: CGO disabled, running coverage without -race"
	go test -coverprofile=coverage.txt ./...
endif
	go tool cover -func=coverage.txt

cover-e2e: ## Run all tests with e2e coverage merged
	@rm -rf .coverdata && mkdir -p .coverdata
	BGTASK_E2E_COVER=1 GOCOVERDIR=$$PWD/.coverdata go test -race -count=1 ./cmd/bgtask/...
	go tool covdata textfmt -i=.coverdata -o=coverage-e2e.txt
	go tool cover -func=coverage-e2e.txt
	@rm -rf .coverdata

lint: ## Run linters
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "warning: golangci-lint not found, skipping (install: https://golangci-lint.run/welcome/install/)"; \
	fi

fmt: ## Format markdown and YAML with prettier
	npx --yes prettier --write .

fmt-check: ## Check formatting (same as CI)
	npx --yes prettier --check .

clean: ## Remove build artifacts
	rm -rf bin/ coverage.txt coverage-e2e.txt

vulncheck: ## Check for known vulnerabilities
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
