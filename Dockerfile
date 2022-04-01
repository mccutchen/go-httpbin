# syntax = docker/dockerfile:1.3
FROM golang:1.18

WORKDIR /go/src/github.com/mccutchen/go-httpbin

COPY . .
RUN --mount=type=cache,id=gobuild,target=/root/.cache/go-build \
    make build buildtests

FROM gcr.io/distroless/base
COPY --from=0 /go/src/github.com/mccutchen/go-httpbin/dist/go-httpbin* /bin/
EXPOSE 8080
CMD ["/bin/go-httpbin"]
