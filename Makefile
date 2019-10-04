.PHONY: clean deploy deps image imagepush lint run stagedeploy test testci testcover

# The version that will be used in docker tags (e.g. to push a
# go-httpbin:latest image use `make imagepush VERSION=latest)`
VERSION        ?= $(shell git rev-parse --short HEAD)

# Override these values to deploy to a different App Engine project
GCLOUD_PROJECT ?= httpbingo
GCLOUD_ACCOUNT ?= mccutchen@gmail.com

# Built binaries will be placed here
DIST_PATH  	  ?= dist

# Default flags used by the test, testci, testcover targets
COVERAGE_PATH ?= coverage.txt
COVERAGE_ARGS ?= -covermode=atomic -coverprofile=$(COVERAGE_PATH)
TEST_ARGS     ?= -race

GENERATED_ASSETS_PATH := httpbin/assets/assets.go

BIN_DIR   := $(GOPATH)/bin
GOLINT    := $(BIN_DIR)/golint
GOBINDATA := $(BIN_DIR)/go-bindata

GO_SOURCES = $(wildcard **/*.go)

# =============================================================================
# build
# =============================================================================
build: $(DIST_PATH)/go-httpbin

$(DIST_PATH)/go-httpbin: assets $(GO_SOURCES)
	mkdir -p $(DIST_PATH)
	go build -o $(DIST_PATH)/go-httpbin ./cmd/go-httpbin

assets: $(GENERATED_ASSETS_PATH)

clean:
	rm -rf $(DIST_PATH) $(COVERAGE_PATH)

$(GENERATED_ASSETS_PATH): $(GOBINDATA) static/*
	$(GOBINDATA) -o $(GENERATED_ASSETS_PATH) -pkg=assets -prefix=static static
	# reformat generated code
	gofmt -s -w $(GENERATED_ASSETS_PATH)
	# dumb hack to make generate code lint correctly
	sed -i.bak 's/Html/HTML/g' $(GENERATED_ASSETS_PATH)
	sed -i.bak 's/Xml/XML/g' $(GENERATED_ASSETS_PATH)
	rm $(GENERATED_ASSETS_PATH).bak


# =============================================================================
# test & lint
# =============================================================================
test:
	go test $(TEST_ARGS) ./...

# Test command to run for continuous integration, which includes code coverage
# based on codecov.io's documentation:
# https://github.com/codecov/example-go/blob/b85638743b972bd0bd2af63421fe513c6f968930/README.md
testci:
	go test $(TEST_ARGS) $(COVERAGE_ARGS) ./...

testcover: testci
	go tool cover -html=$(COVERAGE_PATH)

lint: $(GOLINT)
	test -z "$$(gofmt -d -s -e .)" || (gofmt -d -s -e . ; exit 1)
	$(GOLINT) -set_exit_status ./...
	go vet ./...


# =============================================================================
# deploy & run locally
# =============================================================================
deploy: build
	gcloud --account=$(GCLOUD_ACCOUNT) app deploy --quiet --project=$(GCLOUD_PROJECT) --version=$(VERSION) --promote

stagedeploy: build
	gcloud --account=$(GCLOUD_ACCOUNT) app deploy --quiet --project=$(GCLOUD_PROJECT) --version=$(VERSION) --no-promote

run: build
	$(DIST_PATH)/go-httpbin


# =============================================================================
# docker images
# =============================================================================
image: build
	docker build -t mccutchen/go-httpbin:$(VERSION) .

imagepush: image
	docker push mccutchen/go-httpbin:$(VERSION)


# =============================================================================
# dependencies
# =============================================================================
deps: $(GOLINT) $(GOBINDATA)

# Can't install from working dir because of go mod issues:
#
#     go get -u github.com/kevinburke/go-bindata/...
#     go: finding github.com/kevinburke/go-bindata/... latest
#     go get github.com/kevinburke/go-bindata/...: no matching versions for query "latest"
#
# So we get out of the go modules path to install.
$(GOBINDATA):
	cd /tmp && go get -u github.com/kevinburke/go-bindata/...

$(GOLINT):
	go get -u golang.org/x/lint/golint
