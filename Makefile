GO ?= go
HELM ?= helm
DOCKER ?= docker
NPM ?= npm
VERSION ?= dev
SOURCE_REVISION ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= unknown
CONTROLLER_IMAGE ?= ghcr.io/linkmaq/kube-accelerator-sim-controller
IMAGE_PLATFORMS ?= linux/amd64,linux/arm64
CHART_OCI_REGISTRY ?=
RELEASE_EVIDENCE_DIR ?=
RELEASE_OUTPUT ?= dist/release

VERSION_PACKAGE := github.com/LinkMaq/kube-accelerator-sim/internal/version
LDFLAGS := -X '$(VERSION_PACKAGE).productVersion=$(VERSION)' \
	-X '$(VERSION_PACKAGE).sourceRevision=$(SOURCE_REVISION)' \
	-X '$(VERSION_PACKAGE).buildDate=$(BUILD_DATE)'

.PHONY: architecture build chart-package chart-push chart-verify container-image \
	container-image-local format format-check generate-check module-check \
	docs-build docs-dev release-artifacts test test-race traceability-check verify vet

architecture:
	$(GO) run ./internal/tools/archcheck --root .

build:
	mkdir -p dist
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/kasim ./cmd/kasim
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/kasim-controller ./cmd/kasim-controller
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/kasim-telemetry ./cmd/kasim-telemetry

chart-verify:
	$(HELM) lint charts/kasim-runtime --strict
	@for version in 1.30.14 1.31.14 1.32.13 1.33.13 1.34.10 1.35.7 1.36.3; do \
		$(HELM) template contract charts/kasim-runtime \
			--namespace kasim-system \
			--kube-version "$$version" >/dev/null; \
	done

chart-package: chart-verify
	mkdir -p dist
	$(HELM) package charts/kasim-runtime --destination dist
	shasum -a 256 dist/kasim-runtime-*.tgz > dist/kasim-runtime-checksums.txt
	shasum -a 256 release/inputs.json > dist/sbom-inputs-checksums.txt

chart-push: chart-package
	@test -n "$(CHART_OCI_REGISTRY)" || \
		(printf 'CHART_OCI_REGISTRY is required\\n' >&2; exit 2)
	$(HELM) push dist/kasim-runtime-*.tgz $(CHART_OCI_REGISTRY)

container-image:
	mkdir -p dist
	$(DOCKER) buildx build \
		--platform $(IMAGE_PLATFORMS) \
		--build-arg VERSION=$(VERSION) \
		--build-arg SOURCE_REVISION=$(SOURCE_REVISION) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--tag $(CONTROLLER_IMAGE):$(VERSION) \
		--output type=oci,dest=dist/kasim-controller-$(VERSION).oci.tar \
		.
	shasum -a 256 dist/kasim-controller-$(VERSION).oci.tar \
		> dist/kasim-controller-image-checksums.txt

container-image-local:
	$(DOCKER) build \
		--build-arg VERSION=$(VERSION) \
		--build-arg SOURCE_REVISION=$(SOURCE_REVISION) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--tag $(CONTROLLER_IMAGE):$(VERSION) \
		.

docs-build:
	$(NPM) run docs:build

docs-dev:
	$(NPM) run docs:dev

format:
	gofmt -w $$(find . \
		\( -path './.git' -o -path './dist' -o -path './node_modules' -o -path './vendor' \) -prune \
		-o -type f -name '*.go' -print)

format-check:
	@unformatted="$$(gofmt -l $$(find . \
		\( -path './.git' -o -path './dist' -o -path './node_modules' -o -path './vendor' \) -prune \
		-o -type f -name '*.go' -print))"; \
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

release-artifacts:
	@test -n "$(RELEASE_EVIDENCE_DIR)" || \
		(printf 'RELEASE_EVIDENCE_DIR is required\n' >&2; exit 2)
	$(GO) run ./internal/tools/releasebuild \
		--version "$(VERSION)" \
		--revision "$(SOURCE_REVISION)" \
		--build-date "$(BUILD_DATE)" \
		--evidence-dir "$(RELEASE_EVIDENCE_DIR)" \
		--output "$(RELEASE_OUTPUT)"

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

traceability-check:
	$(GO) run ./internal/tools/traceability --check

vet:
	$(GO) vet ./...

verify: format-check vet test test-race architecture traceability-check chart-verify
