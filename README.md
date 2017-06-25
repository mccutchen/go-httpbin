# go-httpbin

A reasonably complete and well-tested golang port of [Kenneth Reitz][kr]'s
[httpbin][httpbin-org] service, with zero dependencies outside the go stdlib.

[![GoDoc](https://godoc.org/github.com/mccutchen/go-httpbin?status.svg)](https://godoc.org/github.com/mccutchen/go-httpbin)
[![Build Status](https://travis-ci.org/mccutchen/go-httpbin.svg?branch=master)](http://travis-ci.org/mccutchen/go-httpbin)
[![Coverage](http://gocover.io/_badge/github.com/mccutchen/go-httpbin/httpbin?0)](http://gocover.io/github.com/mccutchen/go-httpbin/httpbin)

## Usage

Run as a standalone binary, configured by command line flags or environment
variables:

```
$ go-httpbin -help
Usage of ./dist/go-httpbin:
  -listen string
        Listen address (default ":8080")
  -max-duration duration
        Maximum duration a response may take (default 10s)
  -max-memory int
        Maximum size of request or response, in bytes (default 1048576)
```

## Installation

```
go get github.com/mccutchen/go-httpbin/...
```

## See also

 - [kennethreitz/httpbin][httpbin-repo] — the original Python version, without
   which this knock-off wouldn't exist
 - [ahmetb/go-httpbin][ahmet-go-httpbin] — another golang port

## Development

### Building

```
make
```

### Testing

```
make test
make testcover
```

### Running

```
make run
```

[kr]: https://github.com/kennethreitz
[httpbin-org]: https://httpbin.org/
[httpbin-repo]: https://github.com/kennethreitz/httpbin
[ahmet-go-httpbin]: https://github.com/ahmetb/go-httpbin
