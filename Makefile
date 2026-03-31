BINARY ?= pidof

LOCALBIN ?= $(shell pwd)/bin

GOLANGCI_LINT_VERSION ?= v2.11.4
GORELEASER_VERSION ?= v2.15.0

GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint
GORELEASER ?= $(LOCALBIN)/goreleaser
GOLANGCI_LINT_PINNED ?= $(GOLANGCI_LINT)-$(GOLANGCI_LINT_VERSION)
GORELEASER_PINNED ?= $(GORELEASER)-$(GORELEASER_VERSION)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

.PHONY: help tools fmt vet lint test tidy build release-check release-build validate

##@ General

help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Tooling

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

$(GOLANGCI_LINT_PINNED): | $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT_PINNED),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION),golangci-lint)

$(GOLANGCI_LINT): $(GOLANGCI_LINT_PINNED)
	ln -sf "$(GOLANGCI_LINT_PINNED)" "$(GOLANGCI_LINT)"

$(GORELEASER_PINNED): | $(LOCALBIN)
	$(call go-install-tool,$(GORELEASER_PINNED),github.com/goreleaser/goreleaser/v2,$(GORELEASER_VERSION),goreleaser)

$(GORELEASER): $(GORELEASER_PINNED)
	ln -sf "$(GORELEASER_PINNED)" "$(GORELEASER)"

tools: $(GOLANGCI_LINT) $(GORELEASER) ## Install pinned local tooling binaries.

##@ Quality

fmt: ## Run go fmt across all packages.
	go fmt ./...

vet: ## Run go vet across all packages.
	go vet ./...

lint: $(GOLANGCI_LINT) ## Run golangci-lint checks.
	$(GOLANGCI_LINT) run ./...

test: ## Run unit tests with coverage output.
	go test -coverprofile=coverage.out ./...

tidy: ## Tidy and sync Go module files.
	go mod tidy

##@ Build & Release

build: $(GORELEASER) | $(LOCALBIN) ## Build local target binary via GoReleaser snapshot.
	VERSION=$(VERSION) BUILD_TIME=$(BUILD_TIME) $(GORELEASER) build --clean --snapshot --single-target --id pidof --output $(LOCALBIN)/$(BINARY)

release-check: $(GORELEASER) ## Validate GoReleaser configuration.
	$(GORELEASER) check

release-build: $(GORELEASER) ## Build all snapshot artifacts via GoReleaser.
	VERSION=$(VERSION) BUILD_TIME=$(BUILD_TIME) $(GORELEASER) build --snapshot --clean

validate: release-check release-build ## Run GoReleaser check and snapshot build.

# go-install-tool installs a tool binary at a pinned version path.
# $1: target path with binary name and version suffix
# $2: package to go install
# $3: package version
# $4: installed binary name
define go-install-tool
@set -e; \
if [ ! -f "$(1)" ]; then \
	package="$(2)@$(3)"; \
	echo "Downloading $${package}"; \
	GOBIN="$(LOCALBIN)" go install "$${package}"; \
	mv "$(LOCALBIN)/$(4)" "$(1)"; \
fi
endef
