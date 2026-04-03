BINARY ?= pidof

LOCALBIN ?= $(shell pwd)/bin

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Tooling

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

GOLANGCI_LINT_VERSION ?= v2.11.4
GORELEASER_VERSION ?= v2.15.2
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint
GORELEASER ?= $(LOCALBIN)/goreleaser
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

.PHONY: goreleaser
goreleaser: $(GORELEASER) ## Download goreleaser locally if necessary.
$(GORELEASER): $(LOCALBIN)
	$(call go-install-tool,$(GORELEASER),github.com/goreleaser/goreleaser/v2,$(GORELEASER_VERSION))

.PHONY: tools
tools: $(GOLANGCI_LINT) $(GORELEASER) ## Install pinned local tooling binaries.

##@ Quality

.PHONY: fmt
fmt: ## Run go fmt across all packages.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet across all packages.
	go vet ./...

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Run golangci-lint checks.
	$(GOLANGCI_LINT) run --config=.golangci.yml --timeout 2m ./...

.PHONY: lint-fix
lint-fix: $(GOLANGCI_LINT) ## Run golangci-lint checks with fixes detected by the linters.
	$(GOLANGCI_LINT) run --fix --config=.golangci.yml --timeout 2m ./...

.PHONY: test
test: ## Run unit tests with coverage output.
	go test -coverprofile=coverage.out ./...

.PHONY: tidy
tidy: ## Tidy and sync Go module files.
	go mod tidy

##@ Build & Release

.PHONY: build
build: $(GORELEASER) | $(LOCALBIN) ## Build local target binary via GoReleaser snapshot.
	$(GORELEASER) build --clean --snapshot --single-target --id pidof --output $(LOCALBIN)/$(BINARY)

.PHONY: release-check
release-check: $(GORELEASER) ## Validate GoReleaser configuration.
	$(GORELEASER) check

.PHONY: release-build
release-build: $(GORELEASER) release-check ## Build all snapshot artifacts via GoReleaser.
	$(GORELEASER) build --snapshot --clean

.PHONY: validate
validate: release-build ## Run GoReleaser check and snapshot build.

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef
