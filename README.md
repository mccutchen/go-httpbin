# go-httpbin

A reasonably complete and well-tested golang port of [Kenneth Reitz][kr]'s
[httpbin][httpbin-org] service, with zero dependencies outside the go stdlib.

[![GoDoc](https://pkg.go.dev/badge/github.com/mccutchen/go-httpbin/v2)](https://pkg.go.dev/github.com/mccutchen/go-httpbin/v2)
[![Build status](https://github.com/mccutchen/go-httpbin/actions/workflows/ci.yaml/badge.svg)](https://github.com/mccutchen/go-httpbin/actions/workflows/ci.yaml)
[![Coverage](https://codecov.io/gh/mccutchen/go-httpbin/branch/main/graph/badge.svg)](https://codecov.io/gh/mccutchen/go-httpbin)
[![Docker Pulls](https://badgen.net/docker/pulls/mccutchen/go-httpbin?icon=docker&label=pulls)](https://hub.docker.com/r/mccutchen/go-httpbin/)


## Usage

### Docker/OCI images

Prebuilt images for the `linux/amd64` and `linux/arm64` architectures are
automatically published to these public registries for every tagged release:
- [ghcr.io/mccutchen/go-httpbin][ghcr] (recommended)
- [mccutchen/go-httpbin][docker-hub]

```bash
$ docker run -P ghcr.io/mccutchen/go-httpbin
```

> [!NOTE]
> Prebuilt image versions >= 2.19.0 run as a non-root user by default. See
> [Configuring non-root docker images](#configuring-non-root-docker-images)
> below for details.

### Kubernetes

```
$ kubectl apply -k github.com/mccutchen/go-httpbin/kustomize
```

See `./kustomize` directory for further information

### Standalone binary

Follow the [Installation](#installation) instructions to install go-httpbin as
a standalone binary, or use `go run` to install it on demand:

Examples:

```bash
# Run http server
$ go run github.com/mccutchen/go-httpbin/v2/cmd/go-httpbin@latest -host 127.0.0.1 -port 8081

# Run https server
$ openssl genrsa -out server.key 2048
$ openssl ecparam -genkey -name secp384r1 -out server.key
$ openssl req -new -x509 -sha256 -key server.key -out server.crt -days 3650
$ go run github.com/mccutchen/go-httpbin/v2/cmd/go-httpbin@latest -host 127.0.0.1 -port 8081 -https-cert-file ./server.crt -https-key-file ./server.key
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

### GitHub Actions/Workflows

The 3rd-party [lfreleng-actions/go-httpbin-action][] action is an easy way
to make a local instance of go-httpbin available to other steps in a GitHub
Actions workflow.

## Configuration

go-httpbin can be configured via either command line arguments or environment
variables (or a combination of the two):

| Argument| Env var | Documentation | Default |
| - | - | - | - |
| `-allowed-redirect-domains` | `ALLOWED_REDIRECT_DOMAINS` | Comma-separated list of domains the /redirect-to endpoint will allow | |
| `-exclude-headers` | `EXCLUDE_HEADERS` | Drop platform-specific headers. Comma-separated list of headers key to drop, supporting wildcard suffix matching. For example: `"foo,bar,x-fc-*"` | - |
| `-host` | `HOST` | Host to listen on | 0.0.0.0 |
| `-https-cert-file` | `HTTPS_CERT_FILE` | HTTPS Server certificate file | |
| `-https-key-file` | `HTTPS_KEY_FILE` | HTTPS Server private key file | |
| `-log-format` | `LOG_FORMAT` | Log format (text or json) | text |
| `-log-level` | `LOG_LEVEL` | Logging level (DEBUG, INFO, WARN, ERROR, OFF)  | INFO |
| `-max-body-size` | `MAX_BODY_SIZE` | Maximum size of request or response, in bytes | 1048576 |
| `-max-duration` | `MAX_DURATION` | Maximum duration a response may take | 10s |
| `-port` | `PORT` | Port to listen on | 8080 |
| `-prefix` | `PREFIX` | Prefix of path to listen on (must start with slash and does not end with slash) | |
| `-srv-max-header-bytes` | `SRV_MAX_HEADER_BYTES` | Value to use for the http.Server's MaxHeaderBytes option | 16384 |
| `-srv-read-header-timeout` | `SRV_READ_HEADER_TIMEOUT` | Value to use for the http.Server's ReadHeaderTimeout option | 1s |
| `-srv-read-timeout` | `SRV_READ_TIMEOUT` | Value to use for the http.Server's ReadTimeout option | 5s |
| `-use-real-hostname` | `USE_REAL_HOSTNAME` | Expose real hostname as reported by os.Hostname() in the /hostname endpoint | false |

> [!WARNING]
> These configuration options are dangerous and/or deprecated and should be
> avoided unless backwards compatibility is absolutely required.

| Argument| Env var | Documentation | Default |
| - | - | - | - |
| `-unsafe-allow-dangerous-responses` | `UNSAFE_ALLOW_DANGEROUS_RESPONSES` | Allow endpoints to return unescaped HTML when clients control response Content-Type (enables XSS attacks) | false |

**Notes:**
- Command line arguments take precedence over environment variables.
- See [Production considerations] for recommendations around safe configuration
  of public instances of go-httpbin

#### Configuring non-root docker images

Prebuilt image versions >= 2.19.0 run as a non-root user by default to improve
container security at the cost of additional complexity for some non-standard
deployments:

- To run the go-httpbin image a) on a privileged port (i.e. below 1024) _and_
  b) using the Docker host network, you may need to run the container as root
  in order to enable the `CAP_NET_BIND_SERVICE` capability:

  ```bash
  $ docker run \
    --network host \
    --user root \
    --cap-drop ALL \
    --cap-add CAP_NET_BIND_SERVICE \
    ghcr.io/mccutchen/go-httpbin \
    /bin/go-httpbin -port=80
  ```

- If you enable HTTPS directly in the image, make sure that the certificate
  and private key files are readable by the user running the process:

  ```bash
  $ chmod 644 /tmp/server.crt
  $ chmod 640 /tmp/server.key
  # GID 65532: primary group of the nonroot user in distroless/static:nonroot.
  $ chown root:65532 /tmp/server.crt /tmp/server.key
  ```

## Installation

To add go-httpbin as a dependency to an existing golang project (e.g. for use
in unit tests):

```
go get -u github.com/mccutchen/go-httpbin/v2
```

To install the `go-httpbin` binary:

```
go install github.com/mccutchen/go-httpbin/v2/cmd/go-httpbin@latest
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

5. **Prevent leaking sensitive headers**

   By default, go-httpbin will return any request headers sent by the client
   (and any intermediate proxies) in the response. If go-httpbin is deployed
   into an environment where some incoming request headers might reveal
   sensitive information, use the `-exclude-headers` CLI argument or
   `EXCLUDE_HEADERS` env var to configure a denylist of sensitive header keys.

   For example, the Alibaba Cloud Function Compute platform adds
   [a variety of `x-fc-*` headers][alibaba-headers] to each incoming request,
   some of which might be sensitive. To have go-httpbin filter **all** of these
   headers in its own responses, set:

       EXCLUDE_HEADERS="x-fc-*"

   To have go-httpbin filter only specific headers, you can get more specific:

       EXCLUDE_HEADERS="x-fc-access-key-*,x-fc-security-token,x-fc-region"

## Development

See [DEVELOPMENT.md][].

## Security

See [SECURITY.md][].

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
[alibaba-headers]: https://www.alibabacloud.com/help/en/fc/user-guide/specification-details#section-3f8-5y1-i77
[DEVELOPMENT.md]: ./DEVELOPMENT.md
[docker-hub]: https://hub.docker.com/r/mccutchen/go-httpbin/
[examples/custom-instrumentation]: ./examples/custom-instrumentation/
[ghcr]: https://github.com/mccutchen/go-httpbin/pkgs/container/go-httpbin
[httpbin-org]: https://httpbin.org/
[httpbin-repo]: https://github.com/kennethreitz/httpbin
[httpbingo.org]: https://httpbingo.org/
[kr]: https://github.com/kennethreitz
[mccutchen/httpbingo.org]: https://github.com/mccutchen/httpbingo.org
[Observer]: https://pkg.go.dev/github.com/mccutchen/go-httpbin/v2/httpbin#Observer
[Production considerations]: #production-considerations
[SECURITY.md]: ./SECURITY.md
[zerolog]: https://github.com/rs/zerolog
[lfreleng-actions/go-httpbin-action]: https://github.com/lfreleng-actions/go-httpbin-action/
