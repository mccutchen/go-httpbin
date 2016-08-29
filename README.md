# go-httpbin

A WIP golang port of https://httpbin.org/.

[![Build Status](https://travis-ci.org/mccutchen/go-httpbin.svg?branch=master)](http://travis-ci.org/mccutchen/go-httpbin)

## Testing

```
go test
go test -cover
go test -coverprofile=cover.out && go tool cover -html=cover.out
```

## Running

```
go build && ./go-httpbin
```
