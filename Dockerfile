# syntax = docker/dockerfile:1-experimental
FROM golang:1.16

WORKDIR /go/src/github.com/mccutchen/go-httpbin

# Manually implement the subset of `make deps` we need to build the image
RUN cd /tmp && go get -u github.com/kevinburke/go-bindata/...

COPY . .
RUN --mount=type=cache,id=gobuild,target=/root/.cache/go-build \
    make build buildtests

FROM gcr.io/distroless/base
COPY --from=0 /go/src/github.com/mccutchen/go-httpbin/dist/go-httpbin* /bin/
EXPOSE 8080
CMD ["/bin/go-httpbin"]
