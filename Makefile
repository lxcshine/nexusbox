# NexusBox Makefile

# Project metadata
MODULE := github.com/nexusbox/nexusbox
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "v0.1.0-dev")
COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

# Go settings
GO ?= go
GOFLAGS ?= -v
GOLDFLAGS ?= -X $(MODULE)/pkg/version.Version=$(VERSION) \
             -X $(MODULE)/pkg/version.Commit=$(COMMIT) \
             -X $(MODULE)/pkg/version.BuildDate=$(BUILD_DATE)

# Build output
BIN_DIR := bin
CMD_DIR := cmd

# Tools
CONTROLLER_GEN ?= $(GO) run sigs.k8s.io/controller-tools/cmd/controller-gen
CLIENT_GEN ?= $(GO) run k8s.io/code-generator/cmd/client-gen
LINTER ?= golangci-lint

# Docker
DOCKER ?= docker
IMAGE_PREFIX ?= nexusbox
IMAGE_TAG ?= $(VERSION)

# ============================================================================
# Build
# ============================================================================

.PHONY: build
build: build-manager build-agent build-scheduler

.PHONY: build-manager
build-manager:
	$(GO) build $(GOFLAGS) -ldflags "$(GOLDFLAGS)" -o $(BIN_DIR)/sandbox-manager $(MODULE)/$(CMD_DIR)/sandbox-manager

.PHONY: build-agent
build-agent:
	$(GO) build $(GOFLAGS) -ldflags "$(GOLDFLAGS)" -o $(BIN_DIR)/sandbox-agent $(MODULE)/$(CMD_DIR)/sandbox-agent

.PHONY: build-scheduler
build-scheduler:
	$(GO) build $(GOFLAGS) -ldflags "$(GOLDFLAGS)" -o $(BIN_DIR)/sandbox-scheduler $(MODULE)/$(CMD_DIR)/sandbox-scheduler

# ============================================================================
# Code Generation
# ============================================================================

.PHONY: generate
generate: generate-deepcopy generate-client generate-crds

.PHONY: generate-deepcopy
generate-deepcopy:
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./pkg/apis/..."

.PHONY: generate-client
generate-client:
	$(CLIENT_GEN) \
		--clientset-name versioned \
		--input-base "" \
		--input "$(MODULE)/pkg/apis/sandbox/v1alpha1" \
		--output-package "$(MODULE)/pkg/generated/clientset" \
		--go-header-file "hack/boilerplate.go.txt"

.PHONY: generate-crds
generate-crds:
	$(CONTROLLER_GEN) crd paths="./pkg/apis/..." output:crd:dir=deploy/crds

# ============================================================================
# Lint & Vet
# ============================================================================

.PHONY: lint
lint:
	$(LINTER) run ./...

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: fmt
fmt:
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "Files need formatting:"; gofmt -l .; exit 1)

# ============================================================================
# Test
# ============================================================================

.PHONY: test
test: test-unit test-integration

.PHONY: test-unit
test-unit:
	$(GO) test -race -count=1 -coverprofile=coverage.out ./pkg/...

.PHONY: test-integration
test-integration:
	$(GO) test -race -count=1 -tags=integration ./test/integration/...

.PHONY: test-e2e
test-e2e:
	$(GO) test -race -count=1 -tags=e2e ./test/e2e/...

.PHONY: coverage
coverage: test-unit
	$(GO) tool cover -html=coverage.out -o coverage.html

# ============================================================================
# Docker
# ============================================================================

.PHONY: docker-build
docker-build: docker-build-manager docker-build-agent docker-build-scheduler

.PHONY: docker-build-manager
docker-build-manager:
	$(DOCKER) build -f deploy/docker/Dockerfile.manager \
		-t $(IMAGE_PREFIX)/sandbox-manager:$(IMAGE_TAG) .

.PHONY: docker-build-agent
docker-build-agent:
	$(DOCKER) build -f deploy/docker/Dockerfile.agent \
		-t $(IMAGE_PREFIX)/sandbox-agent:$(IMAGE_TAG) .

.PHONY: docker-build-scheduler
docker-build-scheduler:
	$(DOCKER) build -f deploy/docker/Dockerfile.scheduler \
		-t $(IMAGE_PREFIX)/sandbox-scheduler:$(IMAGE_TAG) .

.PHONY: docker-push
docker-push: docker-build
	$(DOCKER) push $(IMAGE_PREFIX)/sandbox-manager:$(IMAGE_TAG)
	$(DOCKER) push $(IMAGE_PREFIX)/sandbox-agent:$(IMAGE_TAG)
	$(DOCKER) push $(IMAGE_PREFIX)/sandbox-scheduler:$(IMAGE_TAG)

# ============================================================================
# Clean
# ============================================================================

.PHONY: clean
clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html

# ============================================================================
# Dependencies
# ============================================================================

.PHONY: deps
deps:
	$(GO) mod tidy
	$(GO) mod download

.PHONY: verify-deps
verify-deps:
	$(GO) mod verify

# ============================================================================
# Install tools
# ============================================================================

.PHONY: install-tools
install-tools:
	$(GO) install sigs.k8s.io/controller-tools/cmd/controller-gen@latest
	$(GO) install k8s.io/code-generator/cmd/client-gen@latest
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
