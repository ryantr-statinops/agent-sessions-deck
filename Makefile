# Makefile — agent-sessions-deck (asd)
#
# CI uses these targets from the repository root; `vendor` and `clean` are maintenance helpers.
# Build, check, and test targets are offline when the repository's `vendor/` tree
# is present; Go selects it automatically (the module's go directive is >= 1.14).
# `make vendor` is a coordinator-only maintenance target and may fetch missing modules.
# Tests never install or launch an agent CLI and need no agent account/credential.
# Override any variable on the command line, for example:
#   make test PACKAGES=./internal/...
#   make test-integration EXTRA_TEST_FLAGS=-run TestPTY -v

SHELL := /bin/sh
GO ?= go
MAKEFLAGS += --no-print-directory

BIN_DIR ?= bin
BINARY ?= asd
CMD ?= ./cmd/asd
PACKAGES ?= ./...

# Integration tests are the files guarded by `//go:build integration`.
INTEGRATION_TAG ?= integration
TEST_FLAGS ?= -count=1
EXTRA_TEST_FLAGS ?=
EXTRA_BUILD_FLAGS ?=
UNIT_TIMEOUT ?= 5m
INTEGRATION_TIMEOUT ?= 10m
RACE_TIMEOUT ?= 15m

# Build metadata. `cmd/asd` declares buildVersion, buildCommit and
# buildDate; the module path is read from go.mod so this stays correct if the
# module identity is renamed.
MODULE := $(shell $(GO) list -m 2>/dev/null)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
SOURCE_DATE_EPOCH ?=
DATE ?= $(shell if [ -n "$(SOURCE_DATE_EPOCH)" ]; then date -u -d "@$(SOURCE_DATE_EPOCH)" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown; else date -u +%Y-%m-%dT%H:%M:%SZ; fi)
LDFLAGS ?= -s -w \
	-X main.buildVersion=$(VERSION) \
	-X main.buildCommit=$(COMMIT) \
	-X main.buildDate=$(DATE)

# Package directories for gofmt and for the integration-tag probe. The
# integration tag is included so packages that hold *only* integration-tagged
# files are still covered; the default `go list` skips those directories.
GOFMT_DIRS := $(shell $(GO) list -tags=$(INTEGRATION_TAG) -f '{{.Dir}}' ./... 2>/dev/null)

.PHONY: all help build smoke build-arm64 check fmt fmt-check lint test \
	test-integration test-race vendor clean

all: build

##@ General

help: ## List available targets
	@printf '%s\n' 'asd — build and check targets (Linux-first, offline).'
	@printf '%s\n' ''
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'
	@printf '%s\n' ''
	@printf '%s\n' 'Common overrides: PACKAGES, EXTRA_TEST_FLAGS, GO, VERSION, COMMIT, DATE.'

##@ Build

build: ## Build bin/asd for the host platform with build metadata
	@test -n "$(MODULE)" || { echo 'build: cannot read the module path from go.mod' >&2; exit 1; }
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)
	@printf 'build: %s version %s\n' '$(BIN_DIR)/$(BINARY)' '$(VERSION)'

smoke: build ## Run the built binary help and version output
	@$(BIN_DIR)/$(BINARY) --help >/dev/null
	@$(BIN_DIR)/$(BINARY) --version
	@printf 'smoke: --help and --version OK\n'

build-arm64: ## Compile-only check for linux/arm64 (no arm64 runtime is executed)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(EXTRA_BUILD_FLAGS) $(PACKAGES)
	@printf 'build-arm64: linux/arm64 compiles (compile check only, not executed)\n'

##@ Checks

check: fmt-check lint test test-integration smoke ## Run the full local check sequence CI runs (minus race and arm64)
	@printf 'check: OK\n'

fmt: ## Rewrite package sources with gofmt (same file set as fmt-check)
	@test -n "$(GOFMT_DIRS)" || { echo 'fmt: no Go packages found' >&2; exit 1; }
	@gofmt -w $(GOFMT_DIRS)
	@printf 'fmt: gofmt -w applied\n'

fmt-check: ## Fail if any package source is not gofmt-formatted
	@test -n "$(GOFMT_DIRS)" || { echo 'fmt-check: no Go packages found' >&2; exit 1; }
	@unformatted="$$(gofmt -l $(GOFMT_DIRS))"; \
	if [ -n "$$unformatted" ]; then \
		printf 'fmt-check: not gofmt-formatted:\n%s\n' "$$unformatted" >&2; \
		exit 1; \
	fi
	@printf 'fmt-check: OK\n'

lint: ## Run go vet over unit and integration sources
	$(GO) vet -tags=$(INTEGRATION_TAG) $(PACKAGES)
	@printf 'lint: go vet OK\n'

##@ Test

test: ## Unit tests (no agent CLI, no network, no accounts)
	$(GO) test $(TEST_FLAGS) $(EXTRA_TEST_FLAGS) -timeout $(UNIT_TIMEOUT) $(PACKAGES)

test-integration: ## Integration tests (Linux-only, allocate their own PTY)
	@case "$$(uname -s)" in \
		Linux) ;; \
		*) printf 'test-integration: SKIP, Linux only (host is %s)\n' "$$(uname -s)" >&2; exit 0 ;; \
	esac
	@if ! grep -rqsE --include='*_test.go' '^//go:build +([^ ]+ +)*$(INTEGRATION_TAG)($$| )' $(GOFMT_DIRS); then \
		printf 'test-integration: WARNING no %s-tagged test files found, this run only re-executed the unit suite\n' '$(INTEGRATION_TAG)' >&2; \
	fi
	$(GO) test $(TEST_FLAGS) $(EXTRA_TEST_FLAGS) -tags=$(INTEGRATION_TAG) -timeout $(INTEGRATION_TIMEOUT) $(PACKAGES)

test-race: ## Unit and integration tests under the race detector
	$(GO) test $(TEST_FLAGS) $(EXTRA_TEST_FLAGS) -tags=$(INTEGRATION_TAG) -race -timeout $(RACE_TIMEOUT) $(PACKAGES)

##@ Maintenance

vendor: ## Regenerate vendor/ from go.mod (coordinator-only; may fetch missing modules)
	@printf 'vendor: regenerates the tracked offline dependency tree; may fetch missing modules.\n'
	$(GO) mod vendor

clean: ## Remove build output and the test result cache (keeps the Go build cache)
	rm -rf $(BIN_DIR)
	$(GO) clean -testcache
	@printf 'clean: removed %s and the Go test cache; run `go clean -cache` to also drop the build cache\n' '$(BIN_DIR)'
