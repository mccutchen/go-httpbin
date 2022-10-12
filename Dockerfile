# syntax = docker/dockerfile:1.3
# Golang
FROM golang:1.19 as build

WORKDIR /go/src/github.com/mccutchen/go-httpbin

COPY . .

RUN --mount=type=cache,id=gobuild,target=/root/.cache/go-build \
    make build buildtests

# distroless
FROM gcr.io/distroless/base

COPY --from=build /go/src/github.com/mccutchen/go-httpbin/dist/go-httpbin* /bin/

EXPOSE 8080
ENTRYPOINT ["/bin/go-httpbin"]
