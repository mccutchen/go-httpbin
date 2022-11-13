# go-httpbin

A reasonably complete and well-tested golang port of [Kenneth Reitz][kr]'s
[httpbin][httpbin-org] service, with zero dependencies outside the go stdlib.

[![GoDoc](https://pkg.go.dev/badge/github.com/mccutchen/go-httpbin/v2)](https://pkg.go.dev/github.com/mccutchen/go-httpbin/v2)
[![Build status](https://github.com/mccutchen/go-httpbin/actions/workflows/test.yaml/badge.svg)](https://github.com/mccutchen/go-httpbin/actions/workflows/test.yaml)
[![Coverage](https://codecov.io/gh/mccutchen/go-httpbin/branch/main/graph/badge.svg)](https://codecov.io/gh/mccutchen/go-httpbin)
[![Docker Pulls](https://badgen.net/docker/pulls/mccutchen/go-httpbin?icon=docker&label=pulls)](https://hub.docker.com/r/mccutchen/go-httpbin/)


## Usage

### Docker

Docker images are published to [Docker Hub][docker-hub]:

```bash
# Run http server
$ docker run -P mccutchen/go-httpbin

# Run https server
$ docker run -e HTTPS_CERT_FILE='/tmp/server.crt' -e HTTPS_KEY_FILE='/tmp/server.key' -p 8080:8080 -v /tmp:/tmp mccutchen/go-httpbin
```

### Standalone binary

Follow the [Installation](#installation) instructions to install go-httpbin as
a standalone binary. (This currently requires a working Go runtime.)

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

### Unit testing helper library

The `github.com/mccutchen/go-httpbin/httpbin/v2` package can also be used as a
library for testing an application's interactions with an upstream HTTP
service, like so:

```go
package httpbin_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/mccutchen/go-httpbin/v2/httpbin"
)

func TestSlowResponse(t *testing.T) {
	app := httpbin.New()
	testServer := httptest.NewServer(app)
	defer testServer.Close()

	client := http.Client{
		Timeout: time.Duration(1 * time.Second),
	}

	_, err := client.Get(testServer.URL + "/delay/10")
	if !os.IsTimeout(err) {
		t.Fatalf("expected timeout error, got %s", err)
	}
}
```

### Configuration

go-httpbin can be configured via either command line arguments or environment
variables (or a combination of the two):

| Argument| Env var | Documentation | Default |
| - | - | - | - |
| `-allowed-redirect-domains` | `ALLOWED_REDIRECT_DOMAINS` | Comma-separated list of domains the /redirect-to endpoint will allow | |
| `-host` | `HOST` | Host to listen on | "0.0.0.0" |
| `-https-cert-file` | `HTTPS_CERT_FILE` | HTTPS Server certificate file | |
| `-https-key-file` | `HTTPS_KEY_FILE` | HTTPS Server private key file | |
| `-max-body-size` | `MAX_BODY_SIZE` | Maximum size of request or response, in bytes | 1048576 |
| `-max-duration` | `MAX_DURATION` | Maximum duration a response may take | 10s |
| `-port` | `PORT` | Port to listen on | 8080 |
| `-use-real-hostname` | `USE_REAL_HOSTNAME` | Expose real hostname as reported by os.Hostname() in the /hostname endpoint | false |

**Notes:**
- Command line arguments take precedence over environment variables.
- See [Production considerations] for recommendations around safe configuration
  of public instances of go-httpbin


## Installation

To add go-httpbin to an existing golang project:

```
go get -u github.com/mccutchen/go-httpbin/v2
```

To install the `go-httpbin` binary:

```
go install github.com/mccutchen/go-httpbin/v2/cmd/go-httpbin
```


## Production considerations

Before deploying an instance of go-httpbin on your own infrastructure on the
public internet, consider tuning it appropriately:

1. **Restrict the domains to which the `/redirect-to` endpoint will send
   traffic to avoid the security issues of an open redirect**

   Use the `-allowed-redirect-domains` CLI argument or the
   `ALLOWED_REDIRECT_DOMAINS` env var to configure an appropriate allowlist.

2. **Tune per-request limits**

   Because go-httpbin allows clients send arbitrary data in request bodies and
   control the duration some requests (e.g. `/delay/60s`), it's important to
   properly tune limits to prevent misbehaving or malicious clients from taking
   too many resources.

   Use the `-max-body-size`/`MAX_BODY_SIZE` and `-max-duration`/`MAX_DURATION`
   CLI arguments or env vars to enforce appropriate limits on each request.

3. **Decide whether to expose real hostnames in the `/hostname` endpoint**

   By default, the `/hostname` endpoint serves a dummy hostname value, but it
   can be configured to serve the real underlying hostname (according to
   `os.Hostname()`) using the `-use-real-hostname` CLI argument or the
   `USE_REAL_HOSTNAME` env var to enable this functionality.

   Before enabling this, ensure that your hostnames do not reveal too much
   about your underlying infrastructure.

4. **Add custom instrumentation**

   By default, go-httpbin logs basic information about each request. To add
   more detailed instrumentation (metrics, structured logging, request
   tracing), you'll need to wrap this package in your own code, which you can
   then instrument as you would any net/http server. Some examples:

   - [examples/custom-instrumentation] instruments every request using DataDog,
     based on the built-in [Observer] mechanism.

   - [mccutchen/httpbingo.org] is the code that powers the public instance of
     go-httpbin deployed to [httpbingo.org], which adds customized structured
     logging using [zerolog] and further hardens the HTTP server against
     malicious clients by tuning lower-level timeouts and limits.

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


[ahmet]: https://github.com/ahmetb/go-httpbin
[docker-hub]: https://hub.docker.com/r/mccutchen/go-httpbin/
[examples/custom-instrumentation]: ./examples/custom-instrumentation/
[httpbin-org]: https://httpbin.org/
[httpbin-repo]: https://github.com/kennethreitz/httpbin
[httpbingo.org]: https://httpbingo.org/
[kr]: https://github.com/kennethreitz
[mccutchen/httpbingo.org]: https://github.com/mccutchen/httpbingo.org
[Observer]: https://pkg.go.dev/github.com/mccutchen/go-httpbin/v2/httpbin#Observer
[Production considerations]: #production-considerations
[zerolog]: https://github.com/rs/zerolog
