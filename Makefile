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

# 3rd party tools
GOLINT      := go run golang.org/x/lint/golint@latest
REFLEX      := go run github.com/cespare/reflex@v0.3.1
STATICCHECK := go run honnef.co/go/tools/cmd/staticcheck@2023.1.3


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

lint:
	test -z "$$(gofmt -d -s -e .)" || (echo "Error: gofmt failed"; gofmt -d -s -e . ; exit 1)
	go vet ./...
	$(GOLINT) -set_exit_status ./...
	$(STATICCHECK) ./...
.PHONY: lint


# =============================================================================
# run locally
# =============================================================================
run: build
	$(DIST_PATH)/go-httpbin
.PHONY: run

watch:
	$(REFLEX) -s -r '\.(go|html)$$' make run
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
