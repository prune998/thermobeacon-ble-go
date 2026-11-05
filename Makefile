# Makefile for thermobeacon-ble-go
#
# Mirrors the five release targets of .goreleaser.yaml:
#   - darwin/amd64 + darwin/arm64 with CGO (CoreBluetooth via tinygo/cbgo);
#     darwin/amd64 cross compiles from an arm64 Mac, Apple clang handles both
#     architectures
#   - linux/amd64, linux/arm64, linux/armv7 with CGO disabled - statically
#     linked, ideal for Venus OS (Cerbo GX)
#
# darwin targets require a macOS host (cgo needs the CoreBluetooth SDK); the
# linux targets cross compile from anywhere with CGO disabled.
# Windows is not part of the supported release matrix.
#
# Releases are still cut with GoReleaser (see .github/workflows/release.yml):
#   make release-check      validate .goreleaser.yaml
#   make release-snapshot   local dry run into dist/ (no publish)

BINARY := thermobeacon-ble-go
GO     ?= go

# git tag or commit hash; falls back to "dev" outside a git checkout
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

BUILD_DIR := build
GOFLAGS   := -trimpath
LDFLAGS   := -s -w -X main.version=$(VERSION)

HOST_OS := $(shell uname -s)

LINUX_TARGETS  := linux-amd64 linux-arm64 linux-armv7
DARWIN_TARGETS := darwin-amd64 darwin-arm64

# darwin builds need a macOS host (cgo + CoreBluetooth)
ifeq ($(HOST_OS),Darwin)
BUILDABLE_TARGETS := $(LINUX_TARGETS) $(DARWIN_TARGETS)
else
BUILDABLE_TARGETS := $(LINUX_TARGETS)
endif

.DEFAULT_GOAL := help
.PHONY: help build clean check fmt vet test install release-check release-snapshot \
        $(addprefix build-,$(LINUX_TARGETS) $(DARWIN_TARGETS))

help: ## list available targets
	@echo "thermobeacon-ble-go $(VERSION) (host: $(HOST_OS))"
	@echo
	@echo "  make build              build for every platform this host can target"
	@echo "  make build-linux-amd64  single platform builds:"
	@echo "  make build-linux-arm64    $(LINUX_TARGETS)"
	@echo "  make build-linux-armv7    $(DARWIN_TARGETS) (macOS host only)"
	@echo "  make build-darwin-amd64"
	@echo "  make build-darwin-arm64"
	@echo
	@echo "  make check              gofmt + go vet + go test (same as CI)"
	@echo "  make test               go test ./..."
	@echo "  make install            go install with the version stamped"
	@echo "  make release-check      goreleaser config validation"
	@echo "  make release-snapshot   goreleaser dry run into dist/ (no publish)"
	@echo "  make clean              remove build/ and dist/"
	@echo
	@echo "outputs land in $(BUILD_DIR)/<os>-<arch>/$(BINARY)"

build: $(addprefix build-,$(BUILDABLE_TARGETS)) ## build all platforms buildable on this host
	@echo "done:"
	@for t in $(BUILDABLE_TARGETS); do echo "  $(BUILD_DIR)/$$t/$(BINARY)"; done
ifneq ($(HOST_OS),Darwin)
	@echo "note: darwin targets skipped (need a macOS host, see .goreleaser.yaml)"
endif

build-linux-amd64: ## build linux/amd64 (static)
	@echo "==> $@ (version $(VERSION))"
	@mkdir -p $(BUILD_DIR)/linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/linux-amd64/$(BINARY) .

build-linux-arm64: ## build linux/arm64 (static)
	@echo "==> $@ (version $(VERSION))"
	@mkdir -p $(BUILD_DIR)/linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/linux-arm64/$(BINARY) .

build-linux-armv7: ## build linux/armv7 (static, Cerbo GX)
	@echo "==> $@ (version $(VERSION))"
	@mkdir -p $(BUILD_DIR)/linux-armv7
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/linux-armv7/$(BINARY) .

ifeq ($(HOST_OS),Darwin)

build-darwin-amd64: ## build darwin/amd64 (cgo, CoreBluetooth)
	@echo "==> $@ (version $(VERSION))"
	@mkdir -p $(BUILD_DIR)/darwin-amd64
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/darwin-amd64/$(BINARY) .

build-darwin-arm64: ## build darwin/arm64 (cgo, CoreBluetooth)
	@echo "==> $@ (version $(VERSION))"
	@mkdir -p $(BUILD_DIR)/darwin-arm64
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/darwin-arm64/$(BINARY) .

else

build-darwin-amd64 build-darwin-arm64:
	@echo "darwin targets need a macOS host (cgo + CoreBluetooth SDK)" >&2; exit 1

endif

check: fmt vet test ## gofmt + vet + test, same as CI

fmt: ## fail if any file needs gofmt
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "these files need gofmt:" >&2; \
		echo "$$unformatted" >&2; \
		exit 1; \
	fi

vet: ## go vet ./...
	$(GO) vet ./...

test: ## go test ./...
	$(GO) test -count=1 ./...

install: ## go install with the version stamped
	$(GO) install -ldflags '$(LDFLAGS)' .

release-check: ## validate .goreleaser.yaml
	goreleaser check

release-snapshot: ## goreleaser dry run into dist/ (no publish)
	goreleaser release --snapshot --clean

clean: ## remove build/ and dist/
	rm -rf $(BUILD_DIR) dist
