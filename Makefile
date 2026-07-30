GO ?= go
VERSION ?= dev
SOURCE_REVISION ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= unknown

VERSION_PACKAGE := github.com/LinkMaq/kube-accelerator-sim/internal/version
LDFLAGS := -X '$(VERSION_PACKAGE).productVersion=$(VERSION)' \
	-X '$(VERSION_PACKAGE).sourceRevision=$(SOURCE_REVISION)' \
	-X '$(VERSION_PACKAGE).buildDate=$(BUILD_DATE)'

.PHONY: architecture build format format-check generate-check module-check test test-race verify vet

architecture:
	$(GO) run ./internal/tools/archcheck --root .

build:
	mkdir -p dist
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/kasim ./cmd/kasim
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/kasim-controller ./cmd/kasim-controller

format:
	gofmt -w $$(find . -type f -name '*.go' -not -path './vendor/*')

format-check:
	@unformatted="$$(gofmt -l $$(find . -type f -name '*.go' -not -path './vendor/*'))"; \
	if test -n "$$unformatted"; then \
		printf 'gofmt required:\\n%s\\n' "$$unformatted"; \
		exit 1; \
	fi

generate-check:
	$(GO) generate ./...
	git diff --exit-code

module-check:
	$(GO) mod tidy
	git diff --exit-code -- go.mod go.sum

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

verify: format-check vet test test-race architecture
