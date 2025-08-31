.PHONY: all build test coverage test-integration clean lint fmt deps tools install run help

# Variables
BINARY_NAME=lgtmcp
BINARY_PATH=bin/$(BINARY_NAME)
MAIN_PATH=./cmd/lgtmcp
COVERAGE_FILE=coverage.out
COVERAGE_HTML=coverage.html

# Go commands
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod
GOINSTALL=$(GOCMD) install
GOFUMPT=bin/gofumpt
GOLINT=bin/golangci-lint

# Build flags
LDFLAGS=-ldflags "-s -w"

# Default target
all: deps fmt lint test build

# Help target
help:
	@echo "Available targets:"
	@echo "  deps          - Download and verify dependencies"
	@echo "  build         - Build the binary"
	@echo "  test          - Run unit tests"
	@echo "  coverage      - Run tests with coverage report"
	@echo "  test-integration - Run integration tests"
	@echo "  test-all      - Run all tests with coverage"
	@echo "  lint          - Run golangci-lint"
	@echo "  fmt           - Format code with gofumpt"
	@echo "  clean         - Remove built binaries and test artifacts"
	@echo "  install       - Install the binary to GOPATH/bin"
	@echo "  run           - Run the application"

# Install tools locally
tools:
	@echo "==> Installing tools..."
	@mkdir -p bin
	@GOBIN=$(PWD)/bin $(GOCMD) install mvdan.cc/gofumpt@latest
	@GOBIN=$(PWD)/bin $(GOCMD) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.4.0
	@echo "Tools installed to bin/"

# Download dependencies
deps: tools
	@echo "==> Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy
	$(GOMOD) verify

# Build the binary
build: deps
	@echo "==> Building $(BINARY_NAME)..."
	@mkdir -p bin
	$(GOBUILD) $(LDFLAGS) -o $(BINARY_PATH) $(MAIN_PATH)
	@echo "Binary built at $(BINARY_PATH)"

# Run tests
test:
	@echo "==> Running unit tests..."
	$(GOTEST) -short -race -timeout 30s ./...

# Run tests with coverage
coverage:
	@echo "==> Running tests with coverage..."
	$(GOTEST) -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
	@echo "==> Generating coverage report..."
	$(GOCMD) tool cover -func=$(COVERAGE_FILE)
	@coverage=$$(go tool cover -func=$(COVERAGE_FILE) | grep total | awk '{print $$3}' | sed 's/%//'); \
	echo "Total coverage: $$coverage%"; \
	if [ "$$(echo "$$coverage < 70.0" | bc -l)" = "1" ]; then \
		echo "❌ Coverage is below 70% ($$coverage%)"; \
		echo "Run 'make coverage-html' to see uncovered lines"; \
		exit 1; \
	else \
		echo "✅ Coverage target met ($$coverage%)!"; \
	fi

# Generate HTML coverage report
coverage-html: coverage
	@echo "==> Generating HTML coverage report..."
	$(GOCMD) tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "Coverage report generated at $(COVERAGE_HTML)"

# Run integration tests
test-integration:
	@echo "==> Running integration tests..."
	$(GOTEST) -race -tags=integration -timeout 60s ./test/...

# Run all tests
test-all: test test-integration coverage

# Run linter
lint:
	@echo "==> Running golangci-lint..."
	$(GOLINT) run ./...

# Format code
fmt:
	@echo "==> Formatting code..."
	$(GOFUMPT) -w .

# Clean build artifacts
clean:
	@echo "==> Cleaning..."
	@rm -rf bin/
	@rm -f $(COVERAGE_FILE) $(COVERAGE_HTML)
	@echo "Clean complete"

# Install binary
install: build
	@echo "==> Installing $(BINARY_NAME)..."
	$(GOINSTALL) $(MAIN_PATH)
	@echo "$(BINARY_NAME) installed to $$GOPATH/bin"

# Run the application
run: build
	@echo "==> Running $(BINARY_NAME)..."
	$(BINARY_PATH)

# Development workflow targets
.PHONY: dev check pre-commit

# Quick development build
dev: fmt lint test build

# Check everything before commit
check: deps fmt lint coverage

# Pre-commit checks (matches lefthook)
pre-commit: fmt lint test