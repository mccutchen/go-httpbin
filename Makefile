.PHONY: clean deploy deps gcloud-auth image imagepush lint run stagedeploy test testci testcover

# The version that will be used in docker tags (e.g. to push a
# go-httpbin:latest image use `make imagepush VERSION=latest)`
VERSION ?= $(shell git rev-parse --short HEAD)

# Override these values to deploy to a different Cloud Run project
GCLOUD_PROJECT ?= httpbingo
GCLOUD_ACCOUNT ?= mccutchen@gmail.com
GCLOUD_REGION  ?= us-central1

# The version tag for the Cloud Run deployment (override this to adjust
# pre-production URLs)
GCLOUD_TAG ?= "v-$(VERSION)"

# Run gcloud in a container to avoid needing to install the SDK locally
GCLOUD_COMMAND ?= ./bin/gcloud

# We push docker images to both docker hub and gcr.io
DOCKER_TAG_DOCKERHUB ?= mccutchen/go-httpbin:$(VERSION)
DOCKER_TAG_GCLOUD    ?= gcr.io/$(GCLOUD_PROJECT)/go-httpbin:$(VERSION)

# Built binaries will be placed here
DIST_PATH  	  ?= dist

# Default flags used by the test, testci, testcover targets
COVERAGE_PATH ?= coverage.txt
COVERAGE_ARGS ?= -covermode=atomic -coverprofile=$(COVERAGE_PATH)
TEST_ARGS     ?= -race

# Tool dependencies
TOOL_BIN_DIR     ?= $(shell go env GOPATH)/bin
TOOL_GOBINDATA   := $(TOOL_BIN_DIR)/go-bindata
TOOL_GOLINT      := $(TOOL_BIN_DIR)/golint
TOOL_STATICCHECK := $(TOOL_BIN_DIR)/staticcheck

GO_SOURCES = $(wildcard **/*.go)

GENERATED_ASSETS_PATH := httpbin/assets/assets.go

# =============================================================================
# build
# =============================================================================
build: $(DIST_PATH)/go-httpbin

$(DIST_PATH)/go-httpbin: assets $(GO_SOURCES)
	mkdir -p $(DIST_PATH)
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(DIST_PATH)/go-httpbin ./cmd/go-httpbin

assets: $(GENERATED_ASSETS_PATH)

buildtests:
	CGO_ENABLED=0 go test -ldflags="-s -w" -v -c -o $(DIST_PATH)/go-httpbin.test ./httpbin

clean:
	rm -rf $(DIST_PATH) $(COVERAGE_PATH)

$(GENERATED_ASSETS_PATH): $(TOOL_GOBINDATA) static/*
	$(TOOL_GOBINDATA) -o $(GENERATED_ASSETS_PATH) -pkg=assets -prefix=static -modtime=1601471052 static
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
testci: build
	go test $(TEST_ARGS) $(COVERAGE_ARGS) ./...
	git diff --exit-code

testcover: testci
	go tool cover -html=$(COVERAGE_PATH)

lint: $(TOOL_GOLINT) $(TOOL_STATICCHECK)
	test -z "$$(gofmt -d -s -e .)" || (echo "Error: gofmt failed"; gofmt -d -s -e . ; exit 1)
	go vet ./...
	$(TOOL_GOLINT) -set_exit_status ./...
	$(TOOL_STATICCHECK) ./...


# =============================================================================
# run locally
# =============================================================================
run: build
	$(DIST_PATH)/go-httpbin

watch: $(TOOL_REFLEX)
	reflex -s -r '\.(go|html)$$' make run


# =============================================================================
# deploy to fly.io
# =============================================================================
deploy:
	flyctl deploy --strategy=rolling


# =============================================================================
# deploy to google cloud run
# =============================================================================
deploy-cloud-run: gcloud-auth imagepush
	$(GCLOUD_COMMAND) beta run deploy \
		$(GCLOUD_PROJECT) \
		--image=$(DOCKER_TAG_GCLOUD) \
		--revision-suffix=$(VERSION) \
		--tag=$(GCLOUD_TAG) \
		--project=$(GCLOUD_PROJECT) \
		--region=$(GCLOUD_REGION) \
		--allow-unauthenticated \
		--platform=managed
	$(GCLOUD_COMMAND) run services update-traffic --to-latest

stagedeploy-cloud-run: gcloud-auth imagepush
	$(GCLOUD_COMMAND) beta run deploy \
		$(GCLOUD_PROJECT) \
		--image=$(DOCKER_TAG_GCLOUD) \
		--revision-suffix=$(VERSION) \
		--tag=$(GCLOUD_TAG) \
		--project=$(GCLOUD_PROJECT) \
		--region=$(GCLOUD_REGION) \
		--allow-unauthenticated \
		--platform=managed \
		--no-traffic

gcloud-auth:
	@$(GCLOUD_COMMAND) auth list | grep '^\*' | grep -q $(GCLOUD_ACCOUNT) || $(GCLOUD_COMMAND) auth login $(GCLOUD_ACCOUNT)
	@$(GCLOUD_COMMAND) auth print-access-token | docker login -u oauth2accesstoken --password-stdin https://gcr.io


# =============================================================================
# docker images
# =============================================================================
image:
	DOCKER_BUILDKIT=1 docker build -t $(DOCKER_TAG_DOCKERHUB) .

imagepush:
	docker buildx create --name httpbin
	docker buildx use httpbin
	docker buildx build --push --platform linux/amd64,linux/arm64 -t $(DOCKER_TAG_DOCKERHUB) .
	docker buildx rm httpbin


# =============================================================================
# dependencies
#
# Deps are installed outside of working dir to avoid polluting go modules
# =============================================================================
$(TOOL_GOBINDATA):
	go install github.com/kevinburke/go-bindata/go-bindata@v3.23.0

$(TOOL_GOLINT):
	go install golang.org/x/lint/golint@latest

$(TOOL_REFLEX):
	go install github.com/cespare/reflex@0.3.1

$(TOOL_STATICCHECK):
	go install honnef.co/go/tools/cmd/staticcheck@v0.3.0
