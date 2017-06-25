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
  -max-duration duration
        Maximum duration a response may take (default 10s)
  -max-memory int
        Maximum size of request or response, in bytes (default 1048576)
  -port int
        Port to listen on (default 8080)
```

Docker images are published to [Docker Hub][docker-hub]:

```
$ docker run -P mccutchen/go-httpbin
```

The `github.com/mccutchen/go-httpbin/httpbin` package can also be used as a
library for testing an applications interactions with an upstream HTTP service,
like so:

```go
package httpbin_test

import (
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/mccutchen/go-httpbin/httpbin"
)

func TestSlowResponse(t *testing.T) {
    handler := httpbin.NewHTTPBin().Handler()
    srv := httptest.NewServer(handler)
    defer srv.Close()

    client := http.Client{
        Timeout: time.Duration(1 * time.Second),
    }
    _, err := client.Get(srv.URL + "/delay/10")
    if err == nil {
        t.Fatal("expected timeout error")
    }
}
```


## Installation

```
go get github.com/mccutchen/go-httpbin/...
```


## Inspiration & prior art

I've been a longtime user of [Kenneith Reitz][kr]'s original
[httpbin.org][httpbin-org], and wanted to write a golang port for fun and to
see how far I could get using only the stdlib.

When I started this project, there were a handful of existing and incomplete
golang ports, with the most promising being [ahmetb/go-httpbin][ahmet]. This
project showed me how useful it might be to have an `httpbin` _library_
available for testing golang applications.

### Known differences from other httpbin versions

**Compared to [the original][httpbin-org]**:
 - No `/brotli` endpoint (due to lack of support in Go's stdlib)
 - Response values which may be encoded as either a string or a list of strings
   will always be encoded as a list of strings (e.g. request headers, query
   params, form values)

**Compared to [ahmetb/go-httpbin][ahmet]**:
 - No dependencies on 3rd party packages
 - More complete implementation of endpoints


## Development

```bash
# local development
make
make test
make testcover
make run

# building & pushing docker images
make image
make imagepush
```

[kr]: https://github.com/kennethreitz
[httpbin-org]: https://httpbin.org/
[httpbin-repo]: https://github.com/kennethreitz/httpbin
[ahmet]: https://github.com/ahmetb/go-httpbin
[docker-hub]: https://hub.docker.com/r/mccutchen/go-httpbin/
