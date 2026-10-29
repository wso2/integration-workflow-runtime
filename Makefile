# Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
#
# WSO2 LLC. licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

SHELL := /bin/bash
VERSION ?= 0.1.0-SNAPSHOT
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
EXE := $(if $(filter windows,$(GOOS)),.exe,)
OUT := build
DIST_NAME := wso2-workflow-runtime-$(VERSION)-$(GOOS)-$(GOARCH)
MIN_COVERAGE ?= 75
BINARIES := workflow-engine workflow-engine-sql
# The platforms build-dist.yml releases; keep the two lists in step.
RELEASE_PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

# Go modules of the go.work workspace, relative to the repository root.
MODULES := $(shell go list -m -f '{{if .Main}}{{.Dir}}{{end}}' all | sed 's|^$(CURDIR)/||')

# Tools must run on the toolchain go.work selects, or they cannot read its standard library.
TOOLCHAIN_ENV = GOROOT=$$(go env GOROOT) PATH=$$(go env GOROOT)/bin:$$PATH GOTOOLCHAIN=local

# Modules accepted after a manual license check: one module path per line, then the reason.
LICENSE_IGNORES = $(shell grep -v '^\s*\#' third-party/license-exceptions.txt | awk 'NF {print "--ignore " $$1}')

## Tools: installed once per version into $(TOOL_BIN), as a versioned binary plus an unversioned symlink.
TOOL_BIN ?= $(CURDIR)/$(OUT)/tools
GOLANGCI_LINT_VERSION ?= v2.13.2
GO_LICENSES_VERSION ?= v2.0.1
GOVULNCHECK_VERSION ?= v1.9.0
CYCLONEDX_GOMOD_VERSION ?= v1.12.0
# Each path names its version, so after a version bump the target is missing and make installs the new one.
GOLANGCI_LINT := $(TOOL_BIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
GO_LICENSES := $(TOOL_BIN)/go-licenses-$(GO_LICENSES_VERSION)
GOVULNCHECK := $(TOOL_BIN)/govulncheck-$(GOVULNCHECK_VERSION)
CYCLONEDX_GOMOD := $(TOOL_BIN)/cyclonedx-gomod-$(CYCLONEDX_GOMOD_VERSION)

# go_install_tool installs $(2)@$(3) as $(1) and points the unversioned name at it.
define go_install_tool
@set -e; mkdir -p "$(TOOL_BIN)"; echo "Installing $(2)@$(3)"; link="$(patsubst %-$(3),%,$(1))"; rm -f "$$link"; \
GOOS= GOARCH= GOBIN="$(TOOL_BIN)" go install "$(2)@$(3)"; mv "$$link" "$(1)"; ln -sf "$(notdir $(1))" "$$link"
endef

.PHONY: build engine dist sbom tools vet lint fmt-check test test-coverage test-race licenses license-check \
	vulncheck check test-smoke test-full test-docker install-hooks release clean

build: engine

engine:
	cd engine && CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath \
		-ldflags "-s -w -X main.version=$(VERSION)" -o ../$(OUT)/workflow-engine$(EXE) ./cmd/workflow-engine
	cd engine && CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath \
		-ldflags "-s -w" -o ../$(OUT)/workflow-engine-sql$(EXE) ./cmd/workflow-engine-sql

# One distribution directory and zip for GOOS/GOARCH: the layout every release archive uses.
dist: engine sbom
	rm -rf dist/$(DIST_NAME) && mkdir -p dist/$(DIST_NAME)/bin dist/$(DIST_NAME)/conf
	cp $(OUT)/workflow-engine$(EXE) $(OUT)/workflow-engine-sql$(EXE) dist/$(DIST_NAME)/bin/
	cp distribution/conf/runtime.toml dist/$(DIST_NAME)/conf/
	cp -R LICENSE NOTICE third-party dist/$(DIST_NAME)/
	cp -R $(OUT)/sbom dist/$(DIST_NAME)/
	cd dist && rm -f $(DIST_NAME).zip && zip -qr $(DIST_NAME).zip $(DIST_NAME)

# CycloneDX SBOM per binary for GOOS/GOARCH: the modules and standard library linked in, with their licenses.
sbom: $(CYCLONEDX_GOMOD)
	@mkdir -p $(OUT)/sbom
	@for b in $(BINARIES); do (cd engine && $(TOOLCHAIN_ENV) GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		"$(CYCLONEDX_GOMOD)" app -json -licenses -std -main cmd/$$b \
		-output "$(CURDIR)/$(OUT)/sbom/$$b.cdx.json" .) || exit 1; done

tools: $(GOLANGCI_LINT) $(GO_LICENSES) $(GOVULNCHECK) $(CYCLONEDX_GOMOD)

$(GOLANGCI_LINT):
	$(call go_install_tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
$(GO_LICENSES):
	$(call go_install_tool,$(GO_LICENSES),github.com/google/go-licenses/v2,$(GO_LICENSES_VERSION))
$(GOVULNCHECK):
	$(call go_install_tool,$(GOVULNCHECK),golang.org/x/vuln/cmd/govulncheck,$(GOVULNCHECK_VERSION))
$(CYCLONEDX_GOMOD):
	$(call go_install_tool,$(CYCLONEDX_GOMOD),github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod,$(CYCLONEDX_GOMOD_VERSION))

vet:
	@for m in $(MODULES); do echo "Vetting $$m"; (cd $$m && go vet ./...) || exit 1; done

lint: $(GOLANGCI_LINT)
	@for m in $(MODULES); do echo "Linting $$m"; \
		(cd $$m && "$(GOLANGCI_LINT)" run --config "$(CURDIR)/.golangci.yml" ./...) || exit 1; done

fmt-check:
	@unformatted=$$(gofmt -l -s $(MODULES)); if [[ -n "$$unformatted" ]]; then \
		echo "Unformatted Go files:"; echo "$$unformatted"; exit 1; fi

test:
	@for m in $(MODULES); do (cd $$m && go test ./...) || exit 1; done

# Unit tests with the coverage gate: at least MIN_COVERAGE percent outside the thin cmd/ wrappers.
test-coverage:
	@mkdir -p $(OUT)/coverage
	@for m in $(MODULES); do (cd $$m && go test -covermode=atomic \
		-coverprofile="$(CURDIR)/$(OUT)/coverage/$$(basename $$m).out" ./...) || exit 1; done
	@go run ./tools/cmd/covercheck -min $(MIN_COVERAGE) $(OUT)/coverage/*.out

test-race:
	@for m in $(MODULES); do (cd $$m && go test -race ./...) || exit 1; done

# Each platform links a different set of packages, so the inventory and the check cover every release platform.
licenses: $(GO_LICENSES)
	@rm -f $(OUT)/licenses.csv; mkdir -p $(OUT)
	@for p in $(RELEASE_PLATFORMS); do echo "Licenses for $$p"; (cd engine && $(TOOLCHAIN_ENV) GOOS=$${p%/*} \
		GOARCH=$${p#*/} CGO_ENABLED=0 "$(GO_LICENSES)" report ./cmd/... >> "$(CURDIR)/$(OUT)/licenses.csv") || exit 1; done
	@LC_ALL=C sort -u $(OUT)/licenses.csv > third-party/go-licenses.csv

license-check: $(GO_LICENSES)
	@for p in $(RELEASE_PLATFORMS); do echo "License check for $$p"; (cd engine && $(TOOLCHAIN_ENV) GOOS=$${p%/*} \
		GOARCH=$${p#*/} CGO_ENABLED=0 "$(GO_LICENSES)" check ./cmd/... \
		--disallowed_types=forbidden,restricted,unknown $(LICENSE_IGNORES)) || exit 1; done

vulncheck: $(GOVULNCHECK)
	@for m in $(MODULES); do (cd $$m && $(TOOLCHAIN_ENV) "$(GOVULNCHECK)" ./...) || exit 1; done

check: build vet lint fmt-check test-coverage license-check vulncheck

# Ballerina suites (jBallerina, ballerina/workflow) against the engine binary built here.
test-smoke: engine
	go run ./tools/cmd/balsuite smoke --engine $(OUT)/workflow-engine$(EXE)

test-full: engine
	go run ./tools/cmd/balsuite full --engine $(OUT)/workflow-engine$(EXE)

# The GitHub Actions gates and Ballerina suites in Docker, PostgreSQL and MySQL included; nothing else to install.
# DOCKER_TARGET: all (default), gates, smoke or full. DATABASES: a subset of "sqlite postgresql mysql".
COMPOSE := docker compose -f tests/docker/compose.yml
test-docker:
	@mkdir -p $(OUT)/docker
	@$(COMPOSE) run --rm --build runner $(or $(DOCKER_TARGET),all) "$(or $(DATABASES),sqlite postgresql mysql)"; \
		status=$$?; $(COMPOSE) down --volumes=false --remove-orphans >/dev/null 2>&1; exit $$status

install-hooks:
	@git config --local core.hooksPath .githooks
	@echo "Git hooks installed."

release:
	@bash .github/scripts/release_dist.sh "$(RELEASE_VERSION)" "$(or $(REMOTE),origin)"

clean:
	rm -rf $(OUT) dist
