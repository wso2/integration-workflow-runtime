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
PYTHON ?= python3
VERSION ?= 0.1.0-SNAPSHOT
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
EXE := $(if $(filter windows,$(GOOS)),.exe,)
OUT := build
DIST_NAME := wso2-workflow-runtime-$(VERSION)-$(GOOS)-$(GOARCH)
MIN_COVERAGE ?= 75

# Go modules of the go.work workspace, relative to the repository root.
MODULES := $(shell go list -m -f '{{if .Main}}{{.Dir}}{{end}}' all | sed 's|^$(CURDIR)/||')

GO_LICENSES := github.com/google/go-licenses/v2@v2.0.1
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0

# Tools must run on the toolchain go.work selects, or they cannot read its standard library.
TOOLCHAIN_ENV = GOROOT=$$(go env GOROOT) PATH=$$(go env GOROOT)/bin:$$PATH GOTOOLCHAIN=local

# Modules accepted after a manual license check: one module path per line, then the reason.
LICENSE_IGNORES = $(shell grep -v '^\s*\#' third-party/license-exceptions.txt | awk 'NF {print "--ignore " $$1}')

.PHONY: build engine dist vet lint fmt-check test test-coverage test-race test-scripts licenses license-check \
	vulncheck check test-smoke test-full install-hooks release clean

build: engine

engine:
	cd engine && CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath \
		-ldflags "-s -w -X main.version=$(VERSION)" -o ../$(OUT)/workflow-engine$(EXE) ./cmd/workflow-engine
	cd engine && CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath \
		-ldflags "-s -w" -o ../$(OUT)/workflow-engine-sql$(EXE) ./cmd/workflow-engine-sql

# One distribution directory and zip for GOOS/GOARCH: the layout every release archive uses.
dist: engine
	rm -rf dist/$(DIST_NAME) && mkdir -p dist/$(DIST_NAME)/bin dist/$(DIST_NAME)/conf
	cp $(OUT)/workflow-engine$(EXE) $(OUT)/workflow-engine-sql$(EXE) dist/$(DIST_NAME)/bin/
	cp distribution/conf/runtime.toml dist/$(DIST_NAME)/conf/
	cp -R LICENSE NOTICE third-party dist/$(DIST_NAME)/
	cd dist && rm -f $(DIST_NAME).zip && zip -qr $(DIST_NAME).zip $(DIST_NAME)

vet:
	@for m in $(MODULES); do echo "Vetting $$m"; (cd $$m && go vet ./...) || exit 1; done

lint:
	@for m in $(MODULES); do echo "Linting $$m"; \
		(cd $$m && golangci-lint run --config "$(CURDIR)/.golangci.yml" ./...) || exit 1; done

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
	@$(PYTHON) .github/scripts/check_coverage.py --minimum $(MIN_COVERAGE) $(OUT)/coverage/*.out

test-race:
	@for m in $(MODULES); do (cd $$m && go test -race ./...) || exit 1; done

test-scripts:
	@cd .github/scripts && $(PYTHON) -m unittest discover -p 'test_*.py' -q

licenses:
	cd engine && $(TOOLCHAIN_ENV) go run $(GO_LICENSES) report ./cmd/... > ../third-party/go-licenses.csv

license-check:
	cd engine && $(TOOLCHAIN_ENV) go run $(GO_LICENSES) check ./cmd/... \
		--disallowed_types=forbidden,restricted,unknown $(LICENSE_IGNORES)

vulncheck:
	@for m in $(MODULES); do (cd $$m && $(TOOLCHAIN_ENV) go run $(GOVULNCHECK) ./...) || exit 1; done

check: build vet lint fmt-check test-coverage test-scripts license-check vulncheck

# Ballerina suites (jBallerina, ballerina/workflow) against the engine binary built here.
test-smoke: engine
	$(PYTHON) tests/ballerina/run_suite.py smoke --engine $(OUT)/workflow-engine$(EXE)

test-full: engine
	$(PYTHON) tests/ballerina/run_suite.py full --engine $(OUT)/workflow-engine$(EXE)

install-hooks:
	@git config --local core.hooksPath .githooks
	@echo "Git hooks installed."

release:
	@bash .github/scripts/release_dist.sh "$(RELEASE_VERSION)" "$(or $(REMOTE),origin)"

clean:
	rm -rf $(OUT) dist
