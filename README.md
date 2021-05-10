# go-httpbin

A reasonably complete and well-tested golang port of [Kenneth Reitz][kr]'s
[httpbin][httpbin-org] service, with zero dependencies outside the go stdlib.

[![GoDoc](https://godoc.org/github.com/mccutchen/go-httpbin?status.svg)](https://godoc.org/github.com/mccutchen/go-httpbin)
[![Build Status](https://travis-ci.org/mccutchen/go-httpbin.svg?branch=master)](http://travis-ci.org/mccutchen/go-httpbin)
[![Coverage](https://codecov.io/gh/mccutchen/go-httpbin/branch/master/graph/badge.svg)](https://codecov.io/gh/mccutchen/go-httpbin)


## Usage

Run as a standalone binary, configured by command line flags or environment
variables:

```
go-httpbin --help
Usage of go-httpbin:
  -host string
      Host to listen on (default "0.0.0.0")
  -https-cert-file string
      HTTPS Server certificate file
  -https-key-file string
      HTTPS Server private key file
  -max-body-size int
      Maximum size of request or response, in bytes (default 1048576)
  -max-duration duration
      Maximum duration a response may take (default 10s)
  -port int
      Port to listen on (default 8080)
```

Examples:

```bash
# Run http server
$ go-httpbin -host 127.0.0.1 -port 8081

# Run https server
$ openssl genrsa -out server.key 2048
$ openssl ecparam -genkey -name secp384r1 -out server.key
$ openssl req -new -x509 -sha256 -key server.key -out server.crt -days 3650

$ go-httpbin -host 127.0.0.1 -port 8081 -https-cert-file ./server.crt -https-key-file ./server.key
```

Docker images are published to [Docker Hub][docker-hub]:

```bash
# Run http server
$ docker run -P mccutchen/go-httpbin

# Run https server
$ docker run -e HTTPS_CERT_FILE='/tmp/server.crt' -e HTTPS_KEY_FILE='/tmp/server.key' -p 8080:8080 -v /tmp:/tmp mccutchen/go-httpbin
```

The `github.com/mccutchen/go-httpbin/httpbin/v2` package can also be used as a
library for testing an applications interactions with an upstream HTTP service,
like so:

```go
package httpbin_test

import (
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    httpbin "github.com/mccutchen/go-httpbin/httpbin/v2"
)

func TestSlowResponse(t *testing.T) {
    app := httpbin.New()
    testServer := httptest.NewServer(app.Handler())
    defer testServer.Close()

    client := http.Client{
        Timeout: time.Duration(1 * time.Second),
    }
    _, err := client.Get(testServer.URL + "/delay/10")
    if err == nil {
        t.Fatal("expected timeout error")
    }
}
```


## Installation

To add go-httpbin to an existing golang project:

```
go get -u github.com/mccutchen/go-httpbin/v2
```

To install the `go-httpbin` binary:

```
go install github.com/mccutchen/go-httpbin/v2/cmd/go-httpbin
```


## Motivation & prior art

I've been a longtime user of [Kenneith Reitz][kr]'s original
[httpbin.org][httpbin-org], and wanted to write a golang port for fun and to
see how far I could get using only the stdlib.

When I started this project, there were a handful of existing and incomplete
golang ports, with the most promising being [ahmetb/go-httpbin][ahmet]. This
project showed me how useful it might be to have an `httpbin` _library_
available for testing golang applications.

### Known differences from other httpbin versions

Compared to [the original][httpbin-org]:
 - No `/brotli` endpoint (due to lack of support in Go's stdlib)
 - The `?show_env=1` query param is ignored (i.e. no special handling of
   runtime environment headers)
 - Response values which may be encoded as either a string or a list of strings
   will always be encoded as a list of strings (e.g. request headers, query
   params, form values)

Compared to [ahmetb/go-httpbin][ahmet]:
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
