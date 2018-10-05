.PHONY: clean deps image imagepush run test testcover

COMMIT := $(shell git rev-parse --short HEAD)
GENERATED_ASSETS_PATH := httpbin/assets/assets.go
BIN_DIR := $(GOPATH)/bin
GOLINT := $(BIN_DIR)/golint
GOBINDATA := $(BIN_DIR)/go-bindata
TEST_ARGS := -race

build: dist/go-httpbin

dist/go-httpbin: $(GENERATED_ASSETS_PATH) cmd/go-httpbin/*.go httpbin/*.go
	mkdir -p dist
	go build -o dist/go-httpbin ./cmd/go-httpbin

$(GENERATED_ASSETS_PATH): $(GOBINDATA) static/*
	$(GOBINDATA) -o $(GENERATED_ASSETS_PATH) -pkg=assets -prefix=static static
	# reformat generated code
	gofmt -s -w $(GENERATED_ASSETS_PATH)
	# dumb hack to make generate code lint correctly
	sed -i.bak 's/Html/HTML/g' $(GENERATED_ASSETS_PATH)
	sed -i.bak 's/Xml/XML/g' $(GENERATED_ASSETS_PATH)
	rm $(GENERATED_ASSETS_PATH).bak

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

run: build
	./dist/go-httpbin

clean:
	rm -r dist

image: $(GENERATED_ASSETS_PATH) cmd/go-httpbin/*.go httpbin/*.go
	docker build -t mccutchen/go-httpbin:$(COMMIT) .

imagepush: image
	docker push mccutchen/go-httpbin:$(COMMIT)

assets: $(GENERATED_ASSETS_PATH)

deps: $(GOLINT) $(GOBINDATA)

$(GOLINT):
	go get -u golang.org/x/lint/golint

$(GOBINDATA):
	go get -u github.com/kevinburke/go-bindata/...
