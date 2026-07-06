# ====================================================================================
# Makefile for Mizu
#
# This Makefile provides a set of targets to build, test, and manage the Mizu
# application and its related tools. It includes support for cross-compilation
# and version injection.
# ====================================================================================

# Phony targets are not actual files, but commands to be executed.
.PHONY: all build clean install test coverage linux freebsd help

# ====================================================================================
# Variables
# ====================================================================================

# Binary names
MIZU_SERVER_BINARY ?= mizu-server
MIZU_ADMIN_BINARY ?= mizu-admin

# Mizu is pure Go; disabling cgo yields static binaries and avoids
# depending on a host C toolchain for building and linking.
# Override with e.g. `CGO_ENABLED=1 make build` if cgo is ever needed.
export CGO_ENABLED ?= 0

# Get all .go files recursively
GO_FILES := $(shell find . -type f -name '*.go')

# Version Information
# These variables are used to inject version information into the binaries.
# They can be overridden during the build process, e.g., make build VERSION=v1.0.0
VERSION ?= $(shell git describe --tags --always --dirty --match='v*')
COMMIT ?= $(shell git rev-parse --short HEAD)
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Go linker flags to inject version info
LDFLAGS_VARS = -X 'main.version=${VERSION}' -X 'main.commit=${COMMIT}' -X 'main.date=${DATE}'
LDFLAGS = -ldflags="${LDFLAGS_VARS}"

# ====================================================================================
# Default Target
# ====================================================================================

all: build

# ====================================================================================
# Build Targets
# ====================================================================================

build: bin/$(MIZU_SERVER_BINARY) bin/$(MIZU_ADMIN_BINARY)

# Rule to build a Go binary, optionally cross-compiled.
# Usage: $(call build-binary,<cmd-path>,<binary-name>[,<GOOS>,<GOARCH>])
define build-binary
	@echo "Building $(2)$(if $(3), for $(3)/$(4))..."
	@$(if $(3),GOOS=$(3) GOARCH=$(4)) go build $(LDFLAGS) -o ./bin/$(2) $(1)
endef

bin/$(MIZU_SERVER_BINARY): $(GO_FILES)
	$(call build-binary,./cmd/mizu-server,$(MIZU_SERVER_BINARY))

bin/$(MIZU_ADMIN_BINARY): $(GO_FILES)
	$(call build-binary,./cmd/mizu-admin,$(MIZU_ADMIN_BINARY))

# ====================================================================================
# Installation and Cleaning
# ====================================================================================

install:
	@echo "Installing binaries to GOPATH/bin..."
	@go install ./cmd/mizu-server
	@go install ./cmd/mizu-admin

clean:
	@echo "Cleaning build artifacts..."
	@rm -f ./bin/*

# ====================================================================================
# Testing
# ====================================================================================

test:
	@echo "Running tests..."
	@go test -v ./...

coverage:
	@echo "Running tests with coverage..."
	@go test -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out

# ====================================================================================
# Cross-Compilation
# ====================================================================================

linux:
	$(call build-binary,./cmd/mizu-server,$(MIZU_SERVER_BINARY)-linux-amd64,linux,amd64)
	$(call build-binary,./cmd/mizu-admin,$(MIZU_ADMIN_BINARY)-linux-amd64,linux,amd64)

freebsd:
	$(call build-binary,./cmd/mizu-server,$(MIZU_SERVER_BINARY)-freebsd-amd64,freebsd,amd64)
	$(call build-binary,./cmd/mizu-admin,$(MIZU_ADMIN_BINARY)-freebsd-amd64,freebsd,amd64)

# ====================================================================================
# Help Target
# ====================================================================================

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  all              Build all binaries"
	@echo "  build            Build both the mizu-server and mizu-admin binaries"
	@echo "  install          Install both executables to GOPATH/bin"
	@echo "  clean            Clean build artifacts"
	@echo "  test             Run all tests"
	@echo "  coverage         Run tests and generate a coverage report"
	@echo "  linux            Cross-compile a static pure-Go binary for Linux"
	@echo "  freebsd          Cross-compile a static pure-Go binary for FreeBSD"
	@echo "  help             Display this help message"
