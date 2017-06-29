commit := $(shell git rev-parse --short HEAD)

build: dist/go-httpbin

dist/go-httpbin: assets cmd/go-httpbin/*.go httpbin/*.go
	mkdir -p dist
	go build -o dist/go-httpbin ./cmd/go-httpbin

assets: httpbin/assets/*
	go-bindata -o httpbin/assets/assets.go -pkg=assets -prefix=static static

test: assets
	go test ./...

testcover: assets
	mkdir -p dist
	go test -coverprofile=dist/coverage.out github.com/mccutchen/go-httpbin/httpbin
	go tool cover -html=dist/coverage.out

run: build
	./dist/go-httpbin

clean:
	rm -r dist

deps:
	go get -u github.com/jteeuwen/go-bindata/...

image: assets cmd/go-httpbin/*.go httpbin/*.go
	mkdir -p /tmp/go-httpbin-docker
	cp Dockerfile /tmp/go-httpbin-docker
	GOOS=linux GOARCH=amd64 go build -o /tmp/go-httpbin-docker/go-httpbin ./cmd/go-httpbin
	docker build -t mccutchen/go-httpbin:$(commit) /tmp/go-httpbin-docker

imagepush: image
	docker push mccutchen/go-httpbin:$(commit)
