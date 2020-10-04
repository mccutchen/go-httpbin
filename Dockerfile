FROM golang:1.15

WORKDIR /go/src/github.com/mccutchen/go-httpbin

# Manually implement the subset of `make deps` we need to build the image
RUN cd /tmp && go get -u github.com/kevinburke/go-bindata/...

COPY . .
RUN make build buildtests

FROM gcr.io/distroless/base
COPY --from=0 /go/src/github.com/mccutchen/go-httpbin/dist/go-httpbin* /bin/
EXPOSE 8080
CMD ["/bin/go-httpbin"]
