.PHONY: clean deploy deps image imagepush lint run stagedeploy test testcover

GCLOUD_PROJECT ?= httpbingo
TEST_ARGS      ?= -race
VERSION        ?= $(shell git rev-parse --short HEAD)

GENERATED_ASSETS_PATH := httpbin/assets/assets.go

BIN_DIR   := $(GOPATH)/bin
GOLINT    := $(BIN_DIR)/golint
GOBINDATA := $(BIN_DIR)/go-bindata

# =============================================================================
# build
# =============================================================================
build: dist/go-httpbin

dist/go-httpbin: assets cmd/go_httpbin/*.go httpbin/*.go go.mod
	mkdir -p dist
	go build -o dist/go-httpbin ./cmd/go_httpbin

assets: $(GENERATED_ASSETS_PATH)

clean:
	rm -r dist

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

testcover:
	mkdir -p dist
	go test $(TEST_ARGS) -coverprofile=dist/coverage.out github.com/mccutchen/go-httpbin/httpbin
	go tool cover -html=dist/coverage.out

lint: $(GOLINT)
	test -z "$$(gofmt -d -s -e .)" || (gofmt -d -s -e . ; exit 1)
	$(GOLINT) -set_exit_status ./...
	go vet ./...


# =============================================================================
# deploy & run locally
# =============================================================================
deploy: build
	gcloud app deploy --quiet --project=$(GCLOUD_PROJECT) --version=$(VERSION) --promote

stagedeploy: build
	gcloud app deploy --quiet --project=$(GCLOUD_PROJECT) --version=$(VERSION) --no-promote

run: build
	./dist/go-httpbin


# =============================================================================
# docker images
# =============================================================================
image: assets cmd/go-httpbin/*.go httpbin/*.go
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
