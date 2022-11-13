# The version that will be used in docker tags (e.g. to push a
# go-httpbin:latest image use `make imagepush VERSION=latest)`
VERSION    ?= $(shell git rev-parse --short HEAD)
DOCKER_TAG ?= mccutchen/go-httpbin:$(VERSION)

# Built binaries will be placed here
DIST_PATH  	  ?= dist

# Default flags used by the test, testci, testcover targets
COVERAGE_PATH ?= coverage.txt
COVERAGE_ARGS ?= -covermode=atomic -coverprofile=$(COVERAGE_PATH)
TEST_ARGS     ?= -race

# Tool dependencies
TOOL_BIN_DIR     ?= $(shell go env GOPATH)/bin
TOOL_GOLINT      := $(TOOL_BIN_DIR)/golint
TOOL_STATICCHECK := $(TOOL_BIN_DIR)/staticcheck


# =============================================================================
# build
# =============================================================================
build:
	mkdir -p $(DIST_PATH)
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(DIST_PATH)/go-httpbin ./cmd/go-httpbin
.PHONY: build

buildexamples: build
	./examples/build-all
.PHONY: buildexamples

buildtests:
	CGO_ENABLED=0 go test -ldflags="-s -w" -v -c -o $(DIST_PATH)/go-httpbin.test ./httpbin
.PHONY: buildtests

clean:
	rm -rf $(DIST_PATH) $(COVERAGE_PATH)
.PHONY: clean


# =============================================================================
# test & lint
# =============================================================================
test:
	go test $(TEST_ARGS) ./...
.PHONY: test


# Test command to run for continuous integration, which includes code coverage
# based on codecov.io's documentation:
# https://github.com/codecov/example-go/blob/b85638743b972bd0bd2af63421fe513c6f968930/README.md
testci: build buildexamples
	go test $(TEST_ARGS) $(COVERAGE_ARGS) ./...
	git diff --exit-code
.PHONY: testci

testcover: testci
	go tool cover -html=$(COVERAGE_PATH)
.PHONY: testcover

lint: $(TOOL_GOLINT) $(TOOL_STATICCHECK)
	test -z "$$(gofmt -d -s -e .)" || (echo "Error: gofmt failed"; gofmt -d -s -e . ; exit 1)
	go vet ./...
	$(TOOL_GOLINT) -set_exit_status ./...
	$(TOOL_STATICCHECK) ./...
.PHONY: lint


# =============================================================================
# run locally
# =============================================================================
run: build
	$(DIST_PATH)/go-httpbin
.PHONY: run

watch: $(TOOL_REFLEX)
	reflex -s -r '\.(go|html)$$' make run
.PHONY: watch


# =============================================================================
# docker images
# =============================================================================
image:
	DOCKER_BUILDKIT=1 docker build -t $(DOCKER_TAG) .
.PHONY: image

imagepush:
	docker buildx create --name httpbin
	docker buildx use httpbin
	docker buildx build --push --platform linux/amd64,linux/arm64 -t $(DOCKER_TAG) .
	docker buildx rm httpbin
.PHONY: imagepush


# =============================================================================
# dependencies
#
# Deps are installed outside of working dir to avoid polluting go modules
# =============================================================================
$(TOOL_GOLINT):
	go install golang.org/x/lint/golint@latest

$(TOOL_REFLEX):
	go install github.com/cespare/reflex@0.3.1

$(TOOL_STATICCHECK):
	go install honnef.co/go/tools/cmd/staticcheck@v0.3.0
