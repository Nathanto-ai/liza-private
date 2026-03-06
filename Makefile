.PHONY: build test clean install lint check-testhelpers release package build-all tidy run coverage vnv help

# Binary names
BINARY_NAME=liza
MCP_BINARY_NAME=liza-mcp

# Build variables
VERSION?=0.2.0
GIT_COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-X 'github.com/liza-mas/liza/internal/embedded.Version=$(VERSION)' \
	-X 'github.com/liza-mas/liza/internal/embedded.GitCommit=$(GIT_COMMIT)' \
	-X 'github.com/liza-mas/liza/internal/embedded.BuildDate=$(BUILD_DATE)' \
	-X 'main.Version=$(VERSION)' -X 'main.GitCommit=$(GIT_COMMIT)' -X 'main.BuildDate=$(BUILD_DATE)'"

# Sync embedded files from project root
.PHONY: sync-embedded
sync-embedded:
	@echo "Syncing files to internal/embedded/..."
	@rm -rf internal/embedded/contracts internal/embedded/skills internal/embedded/docs internal/embedded/specs
	@rm -f internal/embedded/claude-settings.json internal/embedded/mcp.json
	@mkdir -p internal/embedded/contracts internal/embedded/skills
	@cp contracts/*.md internal/embedded/contracts/
	@cp -r skills/* internal/embedded/skills/
	@find internal/embedded/skills/ -name "__pycache__" -type d -exec rm -rf {} + 2>/dev/null || true
	@cp claude-settings.json internal/embedded/
	@cp mcp.json internal/embedded/
	@echo "Files synced successfully"

# Build the binaries
build: sync-embedded
	@echo "Building $(BINARY_NAME) (version=$(VERSION), commit=$(GIT_COMMIT), date=$(BUILD_DATE))"
	@go build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/liza
	@echo "Building $(MCP_BINARY_NAME) (version=$(VERSION), commit=$(GIT_COMMIT), date=$(BUILD_DATE))"
	@go build $(LDFLAGS) -o $(MCP_BINARY_NAME) ./cmd/liza-mcp

# Run tests
# IMPORTANT: Always use `make test`, not bare `go test ./...`.
# The sync-embedded step copies contracts/, skills/, claude-settings.json, and mcp.json
# into internal/embedded/ for go:embed. Without these files the embedded package fails to compile.
test: sync-embedded check-testhelpers
	go test -v -race -coverprofile=coverage.out ./...

# Run tests with coverage report
coverage: test
	go tool cover -html=coverage.out

# Clean build artifacts
clean:
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_NAME)-*
	rm -f $(MCP_BINARY_NAME)
	rm -f $(MCP_BINARY_NAME)-*
	rm -f coverage.out
	rm -rf dist
	rm -rf internal/embedded/contracts internal/embedded/skills internal/embedded/docs internal/embedded/specs
	rm -f internal/embedded/claude-settings.json internal/embedded/mcp.json
	go clean

# Install the binaries to /usr/local/bin
install: build
	sudo install -m 755 $(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)
	sudo install -m 755 $(MCP_BINARY_NAME) /usr/local/bin/$(MCP_BINARY_NAME)

# Check that testhelpers package is not imported in production code
# This prevents test utilities from leaking into production binaries and
# ensures clear separation between test and production code. Test helpers
# should only be used in *_test.go files.
check-testhelpers:
	@echo "Checking for testhelpers in production code..."
	@if find . -name "*.go" ! -name "*_test.go" -type f -exec grep -l "internal/testhelpers" {} \; | grep -q .; then \
		echo "ERROR: testhelpers package imported in production code:"; \
		find . -name "*.go" ! -name "*_test.go" -type f -exec grep -l "internal/testhelpers" {} \;; \
		echo ""; \
		echo "The testhelpers package should only be imported in test files (*_test.go)."; \
		echo "This ensures test utilities don't leak into production binaries."; \
		exit 1; \
	fi
	@echo "✓ No testhelpers in production code"

# Run linters
lint: sync-embedded check-testhelpers
	go fmt ./...
	go vet ./...

# Tidy dependencies
tidy:
	go mod tidy

# Run the binary
run: build
	./$(BINARY_NAME)

# Build for multiple platforms
build-all: sync-embedded
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_NAME)-linux-amd64 ./cmd/liza
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_NAME)-darwin-amd64 ./cmd/liza
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_NAME)-darwin-arm64 ./cmd/liza
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_NAME)-windows-amd64.exe ./cmd/liza
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(MCP_BINARY_NAME)-linux-amd64 ./cmd/liza-mcp
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(MCP_BINARY_NAME)-darwin-amd64 ./cmd/liza-mcp
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(MCP_BINARY_NAME)-darwin-arm64 ./cmd/liza-mcp
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(MCP_BINARY_NAME)-windows-amd64.exe ./cmd/liza-mcp

# Create release artifacts
release: clean lint test
	@echo "Building release artifacts..."
	@mkdir -p dist
	@# Build liza for all platforms
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-amd64 ./cmd/liza
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-arm64 ./cmd/liza
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-amd64 ./cmd/liza
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-arm64 ./cmd/liza
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-windows-amd64.exe ./cmd/liza
	@# Build liza-mcp for all platforms
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(MCP_BINARY_NAME)-linux-amd64 ./cmd/liza-mcp
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/$(MCP_BINARY_NAME)-linux-arm64 ./cmd/liza-mcp
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/$(MCP_BINARY_NAME)-darwin-amd64 ./cmd/liza-mcp
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(MCP_BINARY_NAME)-darwin-arm64 ./cmd/liza-mcp
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(MCP_BINARY_NAME)-windows-amd64.exe ./cmd/liza-mcp
	@# Create checksums
	@cd dist && sha256sum * > checksums.txt
	@echo "✓ Release artifacts created in dist/"
	@echo ""
	@echo "Artifacts:"
	@ls -lh dist/
	@echo ""
	@echo "Checksums:"
	@cat dist/checksums.txt

# Create distribution packages (tarballs)
package: release
	@echo "Creating distribution packages..."
	@cd dist && \
		tar -czf $(BINARY_NAME)-$(VERSION)-linux-amd64.tar.gz $(BINARY_NAME)-linux-amd64 $(MCP_BINARY_NAME)-linux-amd64 && \
		tar -czf $(BINARY_NAME)-$(VERSION)-linux-arm64.tar.gz $(BINARY_NAME)-linux-arm64 $(MCP_BINARY_NAME)-linux-arm64 && \
		tar -czf $(BINARY_NAME)-$(VERSION)-darwin-amd64.tar.gz $(BINARY_NAME)-darwin-amd64 $(MCP_BINARY_NAME)-darwin-amd64 && \
		tar -czf $(BINARY_NAME)-$(VERSION)-darwin-arm64.tar.gz $(BINARY_NAME)-darwin-arm64 $(MCP_BINARY_NAME)-darwin-arm64 && \
		zip -q $(BINARY_NAME)-$(VERSION)-windows-amd64.zip $(BINARY_NAME)-windows-amd64.exe $(MCP_BINARY_NAME)-windows-amd64.exe
	@echo "✓ Distribution packages created"
	@ls -lh dist/*.tar.gz dist/*.zip

# Run V&V (Verification & Validation) suite
vnv: sync-embedded
	@bash scripts/vnv.sh

# Help target
help:
	@echo "Available targets:"
	@echo "  build              - Build liza and liza-mcp binaries"
	@echo "  test               - Run tests (includes testhelpers check)"
	@echo "  coverage           - Run tests with coverage report"
	@echo "  clean              - Clean build artifacts"
	@echo "  install            - Install liza and liza-mcp binaries"
	@echo "  lint               - Run linters (includes testhelpers check)"
	@echo "  check-testhelpers  - Verify testhelpers not in production code"
	@echo "  tidy               - Tidy dependencies"
	@echo "  run                - Build and run the liza binary"
	@echo "  build-all          - Build both binaries for multiple platforms"
	@echo "  release            - Create release artifacts (run tests, build all platforms, create checksums)"
	@echo "  package            - Create distribution packages (tarballs and zip files)"
	@echo "  vnv                - Run Verification & Validation suite"
