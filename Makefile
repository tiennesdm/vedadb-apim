# VedaDB API Manager (VAPIM) Makefile
# Build targets for development, testing, deployment, and Docker operations.

# ---------------------------------------------------------------------------
# Variables
# ---------------------------------------------------------------------------

BINARY_NAME := vapim
DOCKER_IMAGE := vedadb-apim
DOCKER_TAG := latest
DOCKER_REGISTRY ?=
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
COMMIT_HASH := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
GO_VERSION := 1.21
LDFLAGS := -ldflags "\
	-s -w \
	-X main.Version=$(DOCKER_TAG) \
	-X main.BuildTime=$(BUILD_TIME) \
	-X main.CommitHash=$(COMMIT_HASH) \
"

# Directories
BUILD_DIR := ./build
CMD_DIR := ./cmd/apim
CONFIG_DIR := ./configs

# Go commands
GOCMD := go
GOBUILD := $(GOCMD) build
GOCLEAN := $(GOCMD) clean
GOTEST := $(GOCMD) test
GOGET := $(GOCMD) get
GOMOD := $(GOCMD) mod
GOVET := $(GOCMD) vet
GOFMT := gofmt
GOLINT := golangci-lint

# Colors for output
BLUE := \033[36m
GREEN := \033[32m
YELLOW := \033[33m
RED := \033[31m
RESET := \033[0m

# ---------------------------------------------------------------------------
# Default Target
# ---------------------------------------------------------------------------

.DEFAULT_GOAL := help

.PHONY: help
help: ## Display available targets
	@echo "$(BLUE)VedaDB API Manager (VAPIM) - Available Targets$(RESET)"
	@echo "================================================"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  $(GREEN)%-20s$(RESET) %s\n", $$1, $$2}'

# ---------------------------------------------------------------------------
# Build Targets
# ---------------------------------------------------------------------------

.PHONY: build
build: ## Build the binary for current platform
	@echo "$(BLUE)Building $(BINARY_NAME)...$(RESET)"
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)
	@echo "$(GREEN)Build complete: $(BUILD_DIR)/$(BINARY_NAME)$(RESET)"

.PHONY: build-linux
build-linux: ## Build for Linux AMD64
	@echo "$(BLUE)Building for Linux AMD64...$(RESET)"
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 $(CMD_DIR)
	@echo "$(GREEN)Build complete: $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64$(RESET)"

.PHONY: build-linux-arm64
build-linux-arm64: ## Build for Linux ARM64
	@echo "$(BLUE)Building for Linux ARM64...$(RESET)"
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 $(CMD_DIR)
	@echo "$(GREEN)Build complete: $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64$(RESET)"

.PHONY: build-darwin
build-darwin: ## Build for macOS AMD64
	@echo "$(BLUE)Building for macOS AMD64...$(RESET)"
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 $(CMD_DIR)
	@echo "$(GREEN)Build complete: $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64$(RESET)"

.PHONY: build-darwin-arm64
build-darwin-arm64: ## Build for macOS ARM64 (Apple Silicon)
	@echo "$(BLUE)Building for macOS ARM64...$(RESET)"
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 $(CMD_DIR)
	@echo "$(GREEN)Build complete: $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64$(RESET)"

.PHONY: build-windows
build-windows: ## Build for Windows AMD64
	@echo "$(BLUE)Building for Windows AMD64...$(RESET)"
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe $(CMD_DIR)
	@echo "$(GREEN)Build complete: $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe$(RESET)"

.PHONY: build-all
build-all: build-linux build-linux-arm64 build-darwin build-darwin-arm64 build-windows ## Build for all platforms

# ---------------------------------------------------------------------------
# Development Targets
# ---------------------------------------------------------------------------

.PHONY: run
run: build ## Build and run the server
	@echo "$(BLUE)Starting VedaDB API Manager...$(RESET)"
	$(BUILD_DIR)/$(BINARY_NAME) --config $(CONFIG_DIR)/config.yaml

.PHONY: run-dev
run-dev: ## Run in development mode with hot reload
	@echo "$(BLUE)Starting in development mode...$(RESET)"
	$(GOCMD) run $(CMD_DIR)/main.go --config $(CONFIG_DIR)/config.yaml --log-level debug

.PHONY: clean
clean: ## Remove build artifacts
	@echo "$(BLUE)Cleaning build artifacts...$(RESET)"
	$(GOCLEAN)
	rm -rf $(BUILD_DIR)
	@echo "$(GREEN)Clean complete$(RESET)"

.PHONY: fmt
fmt: ## Format all Go files
	@echo "$(BLUE)Formatting Go files...$(RESET)"
	$(GOFMT) -w -s ./

.PHONY: vet
vet: ## Run go vet
	@echo "$(BLUE)Running go vet...$(RESET)"
	$(GOVET) ./...

.PHONY: lint
lint: ## Run golangci-lint
	@echo "$(BLUE)Running linter...$(RESET)"
	$(GOLINT) run ./...

# ---------------------------------------------------------------------------
# Test Targets
# ---------------------------------------------------------------------------

.PHONY: test
test: ## Run all tests
	@echo "$(BLUE)Running tests...$(RESET)"
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	@echo "$(GREEN)Tests complete$(RESET)"

.PHONY: test-short
test-short: ## Run short tests only
	@echo "$(BLUE)Running short tests...$(RESET)"
	$(GOTEST) -short ./...

.PHONY: test-coverage
test-coverage: test ## Run tests with coverage report
	@echo "$(BLUE)Generating coverage report...$(RESET)"
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "$(GREEN)Coverage report: coverage.html$(RESET)"

.PHONY: test-unit
test-unit: ## Run unit tests only
	@echo "$(BLUE)Running unit tests...$(RESET)"
	$(GOTEST) -v -run "^Test_Unit" ./...

.PHONY: test-integration
test-integration: ## Run integration tests only
	@echo "$(BLUE)Running integration tests...$(RESET)"
	$(GOTEST) -v -run "^Test_Integration" ./...

.PHONY: test-benchmark
test-benchmark: ## Run benchmarks
	@echo "$(BLUE)Running benchmarks...$(RESET)"
	$(GOTEST) -bench=. -benchmem ./...

# ---------------------------------------------------------------------------
# Dependency Targets
# ---------------------------------------------------------------------------

.PHONY: deps
deps: ## Download and verify dependencies
	@echo "$(BLUE)Downloading dependencies...$(RESET)"
	$(GOMOD) download
	$(GOMOD) verify

.PHONY: deps-update
deps-update: ## Update all dependencies
	@echo "$(BLUE)Updating dependencies...$(RESET)"
	$(GOGET) -u ./...
	$(GOMOD) tidy

.PHONY: tidy
tidy: ## Tidy go modules
	@echo "$(BLUE)Tidying modules...$(RESET)"
	$(GOMOD) tidy

# ---------------------------------------------------------------------------
# Docker Targets
# ---------------------------------------------------------------------------

.PHONY: docker-build
docker-build: ## Build Docker image
	@echo "$(BLUE)Building Docker image $(DOCKER_IMAGE):$(DOCKER_TAG)...$(RESET)"
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		--build-arg COMMIT_HASH=$(COMMIT_HASH) \
		--build-arg VERSION=$(DOCKER_TAG) \
		.
	@echo "$(GREEN)Docker image built: $(DOCKER_IMAGE):$(DOCKER_TAG)$(RESET)"

.PHONY: docker-push
docker-push: docker-build ## Push Docker image to registry
	@echo "$(BLUE)Pushing Docker image...$(RESET)"
	@if [ -n "$(DOCKER_REGISTRY)" ]; then \
		docker tag $(DOCKER_IMAGE):$(DOCKER_TAG) $(DOCKER_REGISTRY)/$(DOCKER_IMAGE):$(DOCKER_TAG); \
		docker push $(DOCKER_REGISTRY)/$(DOCKER_IMAGE):$(DOCKER_TAG); \
	else \
		docker push $(DOCKER_IMAGE):$(DOCKER_TAG); \
	fi
	@echo "$(GREEN)Docker image pushed$(RESET)"

.PHONY: docker-run
docker-run: ## Run Docker container
	@echo "$(BLUE)Running Docker container...$(RESET)"
	docker run -d \
		--name $(BINARY_NAME) \
		-p $(shell grep -E "^\s+port:" configs/config.yaml | awk '{print $$2}'):$(shell grep -E "^\s+port:" configs/config.yaml | awk '{print $$2}') \
		-p $(shell echo $$(($(shell grep -E "^\s+port:" configs/config.yaml | awk '{print $$2}') + 1))):$(shell echo $$(($(shell grep -E "^\s+port:" configs/config.yaml | awk '{print $$2}') + 1))) \
		-v $(PWD)/configs:/app/configs \
		-e VAPIM_LOG_LEVEL=info \
		--add-host=host.docker.internal:host-gateway \
		$(DOCKER_IMAGE):$(DOCKER_TAG)
	@echo "$(GREEN)Container started$(RESET)"

.PHONY: docker-stop
docker-stop: ## Stop Docker container
	@echo "$(BLUE)Stopping Docker container...$(RESET)"
	docker stop $(BINARY_NAME) 2>/dev/null || true
	docker rm $(BINARY_NAME) 2>/dev/null || true
	@echo "$(GREEN)Container stopped$(RESET)"

.PHONY: docker-compose-up
docker-compose-up: ## Start with docker-compose
	@echo "$(BLUE)Starting with docker-compose...$(RESET)"
	docker-compose up -d --build
	@echo "$(GREEN)Services started$(RESET)"

.PHONY: docker-compose-down
docker-compose-down: ## Stop docker-compose services
	@echo "$(BLUE)Stopping docker-compose services...$(RESET)"
	docker-compose down
	@echo "$(GREEN)Services stopped$(RESET)"

.PHONY: docker-logs
docker-logs: ## Show Docker container logs
	docker logs -f $(BINARY_NAME)

# ---------------------------------------------------------------------------
# Release Targets
# ---------------------------------------------------------------------------

.PHONY: release
release: clean test build-all ## Build release artifacts for all platforms
	@echo "$(BLUE)Creating release artifacts...$(RESET)"
	@mkdir -p $(BUILD_DIR)/release
	cp $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 $(BUILD_DIR)/release/
	cp $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 $(BUILD_DIR)/release/
	cp $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 $(BUILD_DIR)/release/
	cp $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 $(BUILD_DIR)/release/
	cp $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe $(BUILD_DIR)/release/
	cp $(CONFIG_DIR)/config.yaml $(BUILD_DIR)/release/
	@echo "$(GREEN)Release artifacts created in $(BUILD_DIR)/release/$(RESET)"

# ---------------------------------------------------------------------------
# Utility Targets
# ---------------------------------------------------------------------------

.PHONY: version
version: ## Print version information
	@echo "$(GREEN)VedaDB API Manager$(RESET)"
	@echo "  Version:    $(DOCKER_TAG)"
	@echo "  Go Version: $(GO_VERSION)"
	@echo "  Git Commit: $(COMMIT_HASH)"
	@echo "  Build Time: $(BUILD_TIME)"

.PHONY: check
check: fmt vet test ## Run all checks (format, vet, test)
	@echo "$(GREEN)All checks passed$(RESET)"

.PHONY: install
install: build ## Install binary to GOPATH/bin
	@echo "$(BLUE)Installing $(BINARY_NAME)...$(RESET)"
	cp $(BUILD_DIR)/$(BINARY_NAME) $(GOPATH)/bin/$(BINARY_NAME)
	@echo "$(GREEN)Installed to $(GOPATH)/bin/$(BINARY_NAME)$(RESET)"

.PHONY: generate
generate: ## Generate Go code (mocks, etc.)
	@echo "$(BLUE)Generating code...$(RESET)"
	$(GOCMD) generate ./...

# ---------------------------------------------------------------------------
# Default fallback for undefined targets
# ---------------------------------------------------------------------------

%:
	@echo "$(RED)Unknown target: $@$(RESET)"
	@$(MAKE) help
