FROM gcr.io/distroless/base
COPY go-httpbin /bin/go-httpbin
EXPOSE 8080
CMD ["/bin/go-httpbin"]
