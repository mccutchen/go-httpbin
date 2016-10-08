build: dist/go-httpbin

dist/go-httpbin: *.go httpbin/*.go
	mkdir -p dist
	go build -o dist/go-httpbin

test:
	go test -v github.com/mccutchen/go-httpbin/httpbin

run: build
	./dist/go-httpbin

clean:
	rm -r dist

deps:
	go get -u github.com/jteeuwen/go-bindata/...

.PHONY: all
