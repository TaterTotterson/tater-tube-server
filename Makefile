GO ?= go
DOCKER_IMAGE_TAG ?= latest

.DEFAULT_GOAL := check

.PHONY: proto
proto:
	protoc --go_out=./internal/metadata/proto/ internal/metadata/proto/metadata.proto

.PHONY: generate
generate: proto
	go generate ./...

.PHONY: govulncheck
govulncheck:
	go tool govulncheck ./...

.PHONY: tidy go-mod-tidy
tidy: go-mod-tidy
go-mod-tidy:
	$(GO) mod tidy

.PHONY: golangci-lint golangci-lint-fix
golangci-lint-fix: ARGS=--fix
golangci-lint-fix: golangci-lint
golangci-lint:
	go tool golangci-lint run $(ARGS)

.PHONY: junit
junit: | $(JUNIT)
	mkdir -p ./test-results && $(GO) test -v 2>&1 ./... | go tool go-junit-report -set-exit-code > ./test-results/report.xml

.PHONY: coverage
coverage:
	$(GO) test -v -coverprofile=coverage.out ./...

.PHONY: coverage-html
coverage-html: coverage
	$(GO) tool cover -html=coverage.out -o coverage.html

.PHONY: coverage-func
coverage-func: coverage
	$(GO) tool cover -func=coverage.out

.PHONY: coverage-ci
coverage-ci:
	$(GO) test -v -race -coverprofile=coverage.out -covermode=atomic ./...

.PHONY: coverage-total
coverage-total: coverage
	@$(GO) tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//'

.PHONY: lint
lint: go-mod-tidy golangci-lint

.PHONY: test test-race
test-race: ARGS=-race
test-race: test
test:
	$(GO) test $(ARGS) ./...

.PHONY: check
check: generate go-mod-tidy golangci-lint test-race

.PHONY: git-hooks
git-hooks:
	@echo '#!/bin/sh\nmake' > .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit

.PHONY: docker-build
docker-build:
	docker build -f docker/Dockerfile -t tater-tube-server:$(DOCKER_IMAGE_TAG) .

.PHONY: docker-build-ci
docker-build-ci: build-frontend
	docker build -f docker/Dockerfile.ci -t tater-tube-server:ci-latest .

.PHONY: build-frontend
build-frontend:
	@VERSION=$$(git describe --tags --always --dirty 2>/dev/null || echo "dev"); \
	COMMIT=$$(git rev-parse --short HEAD 2>/dev/null || echo "unknown"); \
	cd frontend && bun install --frozen-lockfile && APP_VERSION=$$VERSION GIT_COMMIT=$$COMMIT bun run build

.PHONY: build-cli
build-cli: build-frontend
	@VERSION=$$(git describe --tags --always --dirty 2>/dev/null || echo "dev"); \
	COMMIT=$$(git rev-parse --short HEAD 2>/dev/null || echo "unknown"); \
	TIMESTAMP=$$(date -u '+%Y-%m-%dT%H:%M:%SZ'); \
	echo "Building tater-tube-server CLI (version: $$VERSION, commit: $$COMMIT)..."; \
	CGO_ENABLED=1 $(GO) build \
		-trimpath \
		-tags=cli \
		-ldflags="-s -w -X 'github.com/TaterTotterson/tater-tube-server/internal/version.Version=$$VERSION' -X 'github.com/TaterTotterson/tater-tube-server/internal/version.GitCommit=$$COMMIT' -X 'github.com/TaterTotterson/tater-tube-server/internal/version.Timestamp=$$TIMESTAMP'" \
		-o tater-tube-server \
		./cmd/tater-tube-server/main.go

.PHONY: build-cli-windows
build-cli-windows: build-frontend
	@VERSION=$$(git describe --tags --always --dirty 2>/dev/null || echo "dev"); \
	COMMIT=$$(git rev-parse --short HEAD 2>/dev/null || echo "unknown"); \
	TIMESTAMP=$$(date -u '+%Y-%m-%dT%H:%M:%SZ'); \
	echo "Building tater-tube-server CLI for Windows (version: $$VERSION, commit: $$COMMIT)..."; \
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
		$(GO) build \
		-trimpath \
		-tags=cli \
		-ldflags="-s -w -X 'github.com/TaterTotterson/tater-tube-server/internal/version.Version=$$VERSION' -X 'github.com/TaterTotterson/tater-tube-server/internal/version.GitCommit=$$COMMIT' -X 'github.com/TaterTotterson/tater-tube-server/internal/version.Timestamp=$$TIMESTAMP'" \
		-o tater-tube-server.exe \
		./cmd/tater-tube-server/main.go
# Prerequisites for Windows build:
#   Cross-compilation (Linux/macOS): MinGW-w64 (apt install gcc-mingw-w64-x86-64 / brew install mingw-w64)
#   Native Windows build: replace CC with your toolchain (MSVC or clang-cl); remove CC=... above
# Regenerate the Windows resource (.syso) files from versioninfo.json + tater-tube-server.exe.manifest.
# The generated cmd/tater-tube-server/resource_windows_*.syso files are committed and the Go linker
# picks them up automatically for GOOS=windows builds — embedding the long-path-aware
# manifest. Re-run this target after editing versioninfo.json or tater-tube-server.exe.manifest.
# Requires: go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest
.PHONY: windows-resources
windows-resources:
	cd cmd/tater-tube-server && goversioninfo -platform-specific versioninfo.json

.PHONY: build
build: build-cli
