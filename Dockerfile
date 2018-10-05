FROM golang:1.11

WORKDIR /go/src/github.com/mccutchen/go-httpbin

COPY Makefile .
RUN make deps

COPY . .
RUN make

FROM gcr.io/distroless/base
COPY --from=0 /go/src/github.com/mccutchen/go-httpbin/dist/go-httpbin /bin/go-httpbin
EXPOSE 8080
CMD ["/bin/go-httpbin"]
