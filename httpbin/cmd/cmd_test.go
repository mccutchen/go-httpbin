package cmd

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/mccutchen/go-httpbin/v2/httpbin"
	"github.com/mccutchen/go-httpbin/v2/internal/testing/assert"
)

// To update, run:
// OSX:
// make && ./dist/go-httpbin -h 2>&1 | pbcopy
// Linux (paste with middle mouse):
// make && ./dist/go-httpbin -h 2>&1 | xclip
const usage = `Usage of go-httpbin:
  -allowed-redirect-domains string
    	Comma-separated list of domains the /redirect-to endpoint will allow
  -exclude-headers string
    	Drop platform-specific headers. Comma-separated list of headers key to drop, supporting wildcard matching.
  -host string
    	Host to listen on (default "0.0.0.0")
  -https-cert-file string
    	HTTPS Server certificate file
  -https-key-file string
    	HTTPS Server private key file
  -log-format string
    	Log format (text or json) (default "text")
  -max-body-size int
    	Maximum size of request or response, in bytes (default 1048576)
  -max-duration duration
    	Maximum duration a response may take (default 10s)
  -port int
    	Port to listen on (default 8080)
  -prefix string
    	Path prefix (empty or start with slash and does not end with slash)
  -srv-max-header-bytes int
    	Value to use for the http.Server's MaxHeaderBytes option (default 16384)
  -srv-read-header-timeout duration
    	Value to use for the http.Server's ReadHeaderTimeout option (default 1s)
  -srv-read-timeout duration
    	Value to use for the http.Server's ReadTimeout option (default 5s)
  -unsafe-allow-dangerous-responses
    	Allow endpoints to return unescaped HTML when clients control response Content-Type (enables XSS attacks)
  -use-real-hostname
    	Expose value of os.Hostname() in the /hostname endpoint instead of dummy value
`

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	testDefaultRealHostname := "real-hostname.test"
	getHostnameDefault := func() (string, error) {
		return testDefaultRealHostname, nil
	}

	defaultCfg, err := loadConfig(nil, func(string) string { return "" }, func() []string { return nil }, getHostnameDefault)
	assert.NilError(t, err)

	testCases := map[string]struct {
		args        []string
		env         map[string]string
		getHostname func() (string, error)
		wantCfg     *config
		wantErr     error
		wantOut     string
	}{
		"defaults": {
			wantCfg: &config{
				ListenHost:           defaultListenHost,
				ListenPort:           defaultListenPort,
				MaxBodySize:          httpbin.DefaultMaxBodySize,
				MaxDuration:          httpbin.DefaultMaxDuration,
				LogFormat:            defaultLogFormat,
				SrvMaxHeaderBytes:    defaultSrvMaxHeaderBytes,
				SrvReadHeaderTimeout: defaultSrvReadHeaderTimeout,
				SrvReadTimeout:       defaultSrvReadTimeout,
			},
		},
		"-h": {
			args:    []string{"-h"},
			wantErr: flag.ErrHelp,
		},
		"-help": {
			args:    []string{"-help"},
			wantErr: flag.ErrHelp,
		},

		// env
		"ok env with empty variables": {
			env:     map[string]string{},
			wantCfg: defaultCfg,
		},
		"ok env with recognized variables": {
			env: map[string]string{
				fmt.Sprintf("%sFOO", defaultEnvPrefix):                     "foo",
				fmt.Sprintf("%s%sBAR", defaultEnvPrefix, defaultEnvPrefix): "bar",
				fmt.Sprintf("%s123", defaultEnvPrefix):                     "123",
			},
			wantCfg: mergedConfig(defaultCfg, &config{
				Env: map[string]string{
					fmt.Sprintf("%sFOO", defaultEnvPrefix):                     "foo",
					fmt.Sprintf("%s%sBAR", defaultEnvPrefix, defaultEnvPrefix): "bar",
					fmt.Sprintf("%s123", defaultEnvPrefix):                     "123",
				},
			}),
		},
		"ok env with unrecognized variables": {
			env:     map[string]string{"HTTPBIN_FOO": "foo", "BAR": "bar"},
			wantCfg: defaultCfg,
		},

		// max body size
		"invalid -max-body-size": {
			args:    []string{"-max-body-size", "foo"},
			wantErr: errors.New("invalid value \"foo\" for flag -max-body-size: parse error"),
		},
		"invalid MAX_BODY_SIZE": {
			env:     map[string]string{"MAX_BODY_SIZE": "foo"},
			wantErr: errors.New("invalid value \"foo\" for env var MAX_BODY_SIZE: parse error"),
		},
		"ok -max-body-size": {
			args: []string{"-max-body-size", "99"},
			wantCfg: mergedConfig(defaultCfg, &config{
				MaxBodySize: 99,
			}),
		},
		"ok MAX_BODY_SIZE": {
			env: map[string]string{"MAX_BODY_SIZE": "9999"},
			wantCfg: mergedConfig(defaultCfg, &config{
				MaxBodySize: 9999,
			}),
		},
		"ok max body size CLI takes precedence over env": {
			args: []string{"-max-body-size", "1234"},
			env:  map[string]string{"MAX_BODY_SIZE": "5678"},
			wantCfg: mergedConfig(defaultCfg, &config{
				MaxBodySize: 1234,
			}),
		},

		// max duration
		"invalid -max-duration": {
			args:    []string{"-max-duration", "foo"},
			wantErr: errors.New("invalid value \"foo\" for flag -max-duration: parse error"),
		},
		"invalid MAX_DURATION": {
			env:     map[string]string{"MAX_DURATION": "foo"},
			wantErr: errors.New("invalid value \"foo\" for env var MAX_DURATION: parse error"),
		},
		"ok -max-duration": {
			args: []string{"-max-duration", "99s"},
			wantCfg: mergedConfig(defaultCfg, &config{
				MaxDuration: 99 * time.Second,
			}),
		},
		"ok MAX_DURATION": {
			env: map[string]string{"MAX_DURATION": "9999s"},
			wantCfg: mergedConfig(defaultCfg, &config{
				MaxDuration: 9999 * time.Second,
			}),
		},
		"ok max duration size CLI takes precedence over env": {
			args: []string{"-max-duration", "1234s"},
			env:  map[string]string{"MAX_DURATION": "5678s"},
			wantCfg: mergedConfig(defaultCfg, &config{
				MaxDuration: 1234 * time.Second,
			}),
		},

		// host
		"ok -host": {
			args: []string{"-host", "192.0.0.1"},
			wantCfg: mergedConfig(defaultCfg, &config{
				ListenHost: "192.0.0.1",
			}),
		},
		"ok HOST": {
			env: map[string]string{"HOST": "192.0.0.2"},
			wantCfg: mergedConfig(defaultCfg, &config{
				ListenHost: "192.0.0.2",
			}),
		},
		"ok host cli takes precedence over end": {
			args: []string{"-host", "99.99.99.99"},
			env:  map[string]string{"HOST": "11.11.11.11"},
			wantCfg: mergedConfig(defaultCfg, &config{
				ListenHost: "99.99.99.99",
			}),
		},

		// port
		"invalid -port": {
			args:    []string{"-port", "foo"},
			wantErr: errors.New("invalid value \"foo\" for flag -port: parse error"),
		},
		"invalid PORT": {
			env:     map[string]string{"PORT": "foo"},
			wantErr: errors.New("invalid value \"foo\" for env var PORT: parse error"),
		},
		"ok -port": {
			args: []string{"-port", "99"},
			wantCfg: mergedConfig(defaultCfg, &config{
				ListenPort: 99,
			}),
		},
		"ok PORT": {
			env: map[string]string{"PORT": "9999"},
			wantCfg: mergedConfig(defaultCfg, &config{
				ListenPort: 9999,
			}),
		},
		"ok port CLI takes precedence over env": {
			args: []string{"-port", "1234"},
			env:  map[string]string{"PORT": "5678"},
			wantCfg: mergedConfig(defaultCfg, &config{
				ListenPort: 1234,
			}),
		},

		// prefix
		"invalid -prefix (does not start with slash)": {
			args:    []string{"-prefix", "invalidprefix1"},
			wantErr: errors.New("Prefix \"invalidprefix1\" must start with a slash"),
		},
		"invalid -prefix (ends with with slash)": {
			args:    []string{"-prefix", "/invalidprefix2/"},
			wantErr: errors.New("Prefix \"/invalidprefix2/\" must not end with a slash"),
		},
		"ok -prefix takes precedence over env": {
			args: []string{"-prefix", "/prefix1"},
			env:  map[string]string{"PREFIX": "/prefix2"},
			wantCfg: mergedConfig(defaultCfg, &config{
				Prefix: "/prefix1",
			}),
		},
		"ok PREFIX": {
			env: map[string]string{"PREFIX": "/prefix2"},
			wantCfg: mergedConfig(defaultCfg, &config{
				Prefix: "/prefix2",
			}),
		},

		// https cert file
		"https cert and key must both be provided, cert only": {
			args:    []string{"-https-cert-file", "/tmp/test.crt"},
			wantErr: errors.New("https cert and key must both be provided"),
		},
		"https cert and key must both be provided, key only": {
			args:    []string{"-https-key-file", "/tmp/test.crt"},
			wantErr: errors.New("https cert and key must both be provided"),
		},
		"ok https CLI": {
			args: []string{
				"-https-cert-file", "/tmp/test.crt",
				"-https-key-file", "/tmp/test.key",
			},
			wantCfg: mergedConfig(defaultCfg, &config{
				TLSCertFile: "/tmp/test.crt",
				TLSKeyFile:  "/tmp/test.key",
			}),
		},
		"ok https env": {
			env: map[string]string{
				"HTTPS_CERT_FILE": "/tmp/test.crt",
				"HTTPS_KEY_FILE":  "/tmp/test.key",
			},
			wantCfg: mergedConfig(defaultCfg, &config{
				TLSCertFile: "/tmp/test.crt",
				TLSKeyFile:  "/tmp/test.key",
			}),
		},
		"ok https CLI takes precedence over env": {
			args: []string{
				"-https-cert-file", "/tmp/cli.crt",
				"-https-key-file", "/tmp/cli.key",
			},
			env: map[string]string{
				"HTTPS_CERT_FILE": "/tmp/env.crt",
				"HTTPS_KEY_FILE":  "/tmp/env.key",
			},
			wantCfg: mergedConfig(defaultCfg, &config{
				TLSCertFile: "/tmp/cli.crt",
				TLSKeyFile:  "/tmp/cli.key",
			}),
		},

		// use-real-hostname
		"ok -use-real-hostname": {
			args: []string{"-use-real-hostname"},
			wantCfg: mergedConfig(defaultCfg, &config{
				RealHostname: testDefaultRealHostname,
			}),
		},
		"ok -use-real-hostname=1": {
			args: []string{"-use-real-hostname", "1"},
			wantCfg: mergedConfig(defaultCfg, &config{
				RealHostname: testDefaultRealHostname,
			}),
		},
		"ok -use-real-hostname=true": {
			args: []string{"-use-real-hostname", "true"},
			wantCfg: mergedConfig(defaultCfg, &config{
				RealHostname: testDefaultRealHostname,
			}),
		},
		// any value for the argument is interpreted as true
		"ok -use-real-hostname=0": {
			args: []string{"-use-real-hostname", "0"},
			wantCfg: mergedConfig(defaultCfg, &config{
				RealHostname: testDefaultRealHostname,
			}),
		},
		"ok USE_REAL_HOSTNAME=1": {
			env: map[string]string{"USE_REAL_HOSTNAME": "1"},
			wantCfg: mergedConfig(defaultCfg, &config{
				RealHostname: testDefaultRealHostname,
			}),
		},
		"ok USE_REAL_HOSTNAME=true": {
			env: map[string]string{"USE_REAL_HOSTNAME": "true"},
			wantCfg: mergedConfig(defaultCfg, &config{
				RealHostname: testDefaultRealHostname,
			}),
		},
		// case sensitive
		"ok USE_REAL_HOSTNAME=TRUE": {
			env:     map[string]string{"USE_REAL_HOSTNAME": "TRUE"},
			wantCfg: defaultCfg,
		},
		"ok USE_REAL_HOSTNAME=false": {
			env:     map[string]string{"USE_REAL_HOSTNAME": "false"},
			wantCfg: defaultCfg,
		},
		"err real hostname error": {
			env:         map[string]string{"USE_REAL_HOSTNAME": "true"},
			getHostname: func() (string, error) { return "", errors.New("hostname error") },
			wantErr:     errors.New("could not look up real hostname: hostname error"),
		},

		// allowed-redirect-domains
		"ok -allowed-redirect-domains": {
			args: []string{"-allowed-redirect-domains", "foo,bar"},
			wantCfg: mergedConfig(defaultCfg, &config{
				AllowedRedirectDomains: []string{"foo", "bar"},
			}),
		},
		"ok ALLOWED_REDIRECT_DOMAINS": {
			env: map[string]string{"ALLOWED_REDIRECT_DOMAINS": "foo,bar"},
			wantCfg: mergedConfig(defaultCfg, &config{
				AllowedRedirectDomains: []string{"foo", "bar"},
			}),
		},
		"ok allowed redirect domains CLI takes precedence over env": {
			args: []string{"-allowed-redirect-domains", "foo.cli,bar.cli"},
			env:  map[string]string{"ALLOWED_REDIRECT_DOMAINS": "foo.env,bar.env"},
			wantCfg: mergedConfig(defaultCfg, &config{
				AllowedRedirectDomains: []string{"foo.cli", "bar.cli"},
			}),
		},
		"ok allowed redirect domains are normalized": {
			args: []string{"-allowed-redirect-domains", "foo, bar  ,, baz   "},
			wantCfg: mergedConfig(defaultCfg, &config{
				AllowedRedirectDomains: []string{"foo", "bar", "baz"},
			}),
		},

		// log-format
		"ok use json log format": {
			args: []string{"-log-format", "json"},
			wantCfg: mergedConfig(defaultCfg, &config{
				LogFormat: "json",
			}),
		},
		"ok use text log format": {
			args: []string{"-log-format", "text"},
			wantCfg: mergedConfig(defaultCfg, &config{
				LogFormat: "text",
			}),
		},
		"ok use json log format using LOG_FORMAT env": {
			env: map[string]string{"LOG_FORMAT": "json"},
			wantCfg: mergedConfig(defaultCfg, &config{
				LogFormat: "json",
			}),
		},

		// srv-max-header-bytes
		"invalid -srv-max-header-bytes": {
			args:    []string{"-srv-max-header-bytes", "foo"},
			wantErr: errors.New("invalid value \"foo\" for flag -srv-max-header-bytes: parse error"),
		},
		"invalid SRV_MAX_HEADER_BYTES": {
			env:     map[string]string{"SRV_MAX_HEADER_BYTES": "foo"},
			wantErr: errors.New("invalid value \"foo\" for env var SRV_MAX_HEADER_BYTES: parse error"),
		},
		"ok -srv-max-header-bytes": {
			args: []string{"-srv-max-header-bytes", "99"},
			wantCfg: mergedConfig(defaultCfg, &config{
				SrvMaxHeaderBytes: 99,
			}),
		},
		"ok SRV_MAX_HEADER_BYTES": {
			env: map[string]string{"SRV_MAX_HEADER_BYTES": "9999"},
			wantCfg: mergedConfig(defaultCfg, &config{
				SrvMaxHeaderBytes: 9999,
			}),
		},
		"ok srv-max-header-bytes CLI takes precedence over SRV_MAX_HEADER_BYTES env": {
			args: []string{"-srv-max-header-bytes", "1234"},
			env:  map[string]string{"SRV_MAX_HEADER_BYTES": "5678"},
			wantCfg: mergedConfig(defaultCfg, &config{
				SrvMaxHeaderBytes: 1234,
			}),
		},

		// srv-read-header-timeout
		"invalid -srv-read-header-timeout": {
			args:    []string{"-srv-read-header-timeout", "foo"},
			wantErr: errors.New("invalid value \"foo\" for flag -srv-read-header-timeout: parse error"),
		},
		"invalid SRV_READ_HEADER_TIMEOUT": {
			env:     map[string]string{"SRV_READ_HEADER_TIMEOUT": "foo"},
			wantErr: errors.New("invalid value \"foo\" for env var SRV_READ_HEADER_TIMEOUT: parse error"),
		},
		"ok -srv-read-header-timeout": {
			args: []string{"-srv-read-header-timeout", "99s"},
			wantCfg: mergedConfig(defaultCfg, &config{
				SrvReadHeaderTimeout: 99 * time.Second,
			}),
		},
		"ok SRV_READ_HEADER_TIMEOUT": {
			env: map[string]string{"SRV_READ_HEADER_TIMEOUT": "9999s"},
			wantCfg: mergedConfig(defaultCfg, &config{
				SrvReadHeaderTimeout: 9999 * time.Second,
			}),
		},
		"ok -srv-read-header-timeout CLI takes precedence over SRV_READ_HEADER_TIMEOUT env": {
			args: []string{"-srv-read-header-timeout", "1234s"},
			env:  map[string]string{"SRV_READ_HEADER_TIMEOUT": "5678s"},
			wantCfg: mergedConfig(defaultCfg, &config{
				SrvReadHeaderTimeout: 1234 * time.Second,
			}),
		},

		// srv-read-timeout
		"invalid -srv-read-timeout": {
			args:    []string{"-srv-read-timeout", "foo"},
			wantErr: errors.New("invalid value \"foo\" for flag -srv-read-timeout: parse error"),
		},
		"invalid SRV_READ_TIMEOUT": {
			env:     map[string]string{"SRV_READ_TIMEOUT": "foo"},
			wantErr: errors.New("invalid value \"foo\" for env var SRV_READ_TIMEOUT: parse error"),
		},
		"ok -srv-read-timeout": {
			args: []string{"-srv-read-timeout", "99s"},
			wantCfg: mergedConfig(defaultCfg, &config{
				SrvReadTimeout: 99 * time.Second,
			}),
		},
		"ok SRV_READ_TIMEOUT": {
			env: map[string]string{"SRV_READ_TIMEOUT": "9999s"},
			wantCfg: mergedConfig(defaultCfg, &config{
				SrvReadTimeout: 9999 * time.Second,
			}),
		},
		"ok -srv-read-timeout CLI takes precedence over SRV_READ_TIMEOUT env": {
			args: []string{"-srv-read-timeout", "1234s"},
			env:  map[string]string{"SRV_READ_TIMEOUT": "5678s"},
			wantCfg: mergedConfig(defaultCfg, &config{
				SrvReadTimeout: 1234 * time.Second,
			}),
		},

		// unsafe-allow-dangerous-responses
		"ok -unsafe-allow-dangerous-responses": {
			args: []string{"-unsafe-allow-dangerous-responses"},
			wantCfg: mergedConfig(defaultCfg, &config{
				UnsafeAllowDangerousResponses: true,
			}),
		},
		"ok -unsafe-allow-dangerous-responses=1": {
			args: []string{"-unsafe-allow-dangerous-responses", "1"},
			wantCfg: mergedConfig(defaultCfg, &config{
				UnsafeAllowDangerousResponses: true,
			}),
		},
		"ok -unsafe-allow-dangerous-responses=true": {
			args: []string{"-unsafe-allow-dangerous-responses", "true"},
			wantCfg: mergedConfig(defaultCfg, &config{
				UnsafeAllowDangerousResponses: true,
			}),
		},
		// any value for the argument is interpreted as true
		"ok -unsafe-allow-dangerous-responses=0": {
			args: []string{"-unsafe-allow-dangerous-responses", "0"},
			wantCfg: mergedConfig(defaultCfg, &config{
				UnsafeAllowDangerousResponses: true,
			}),
		},
		"ok UNSAFE_ALLOW_DANGEROUS_RESPONSES=1": {
			env: map[string]string{"UNSAFE_ALLOW_DANGEROUS_RESPONSES": "1"},
			wantCfg: mergedConfig(defaultCfg, &config{
				UnsafeAllowDangerousResponses: true,
			}),
		},
		"ok UNSAFE_ALLOW_DANGEROUS_RESPONSES=true": {
			env: map[string]string{"UNSAFE_ALLOW_DANGEROUS_RESPONSES": "true"},
			wantCfg: mergedConfig(defaultCfg, &config{
				UnsafeAllowDangerousResponses: true,
			}),
		},
		// case sensitive
		"ok UNSAFE_ALLOW_DANGEROUS_RESPONSES=TRUE": {
			env:     map[string]string{"UNSAFE_ALLOW_DANGEROUS_RESPONSES": "TRUE"},
			wantCfg: defaultCfg,
		},
		"ok UNSAFE_ALLOW_DANGEROUS_RESPONSES=false": {
			env:     map[string]string{"UNSAFE_ALLOW_DANGEROUS_RESPONSES": "false"},
			wantCfg: defaultCfg,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.getHostname == nil {
				tc.getHostname = getHostnameDefault
			}
			cfg, err := loadConfig(tc.args, func(key string) string { return tc.env[key] }, func() []string { return environSlice(tc.env) }, tc.getHostname)

			switch {
			case tc.wantErr != nil && err != nil:
				if tc.wantErr.Error() != err.Error() {
					t.Fatalf("incorrect error\nwant: %q\ngot:  %q", tc.wantErr, err)
				}
			case tc.wantErr != nil:
				t.Fatalf("want error %q, got nil", tc.wantErr)
			case err != nil:
				t.Fatalf("got unexpected error: %q", err)
			}

			if !reflect.DeepEqual(tc.wantCfg, cfg) {
				t.Fatalf("bad config\nwant: %#v\ngot:  %#v", tc.wantCfg, cfg)
			}
		})
	}
}

func TestMainImpl(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		args        []string
		env         map[string]string
		getHostname func() (string, error)
		wantCode    int
		wantOut     string
		wantOutFn   func(t *testing.T, out string)
	}{
		"help": {
			args:     []string{"-h"},
			wantCode: 0,
			wantOut:  usage,
		},
		"cli error": {
			args:     []string{"-max-body-size", "foo"},
			wantCode: 2,
			wantOut:  "error: invalid value \"foo\" for flag -max-body-size: parse error\n\n" + usage,
		},
		"unknown argument": {
			args:     []string{"-zzz"},
			wantCode: 2,
			wantOut:  "error: flag provided but not defined: -zzz\n\n" + usage,
		},
		"real hostname error": {
			args:        []string{"-use-real-hostname"},
			getHostname: func() (string, error) { return "", errors.New("hostname failure") },
			wantCode:    1,
			wantOut:     "error: could not look up real hostname: hostname failure",
		},
		"server error": {
			args: []string{
				"-port", "-256",
				"-host", "127.0.0.1", // default of 0.0.0.0 causes annoying permission popup on macOS
			},
			wantCode: 1,
			wantOutFn: func(t *testing.T, out string) {
				assert.Contains(t, out, `msg="error: listen tcp: address -256: invalid port"`, "server error does not contain expected message")
			},
		},
		"tls cert error": {
			args: []string{
				"-host", "127.0.0.1", // default of 0.0.0.0 causes annoying permission popup on macOS
				"-port", "0",
				"-https-cert-file", "./https-cert-does-not-exist",
				"-https-key-file", "./https-key-does-not-exist",
			},
			wantCode: 1,
			wantOutFn: func(t *testing.T, out string) {
				assert.Contains(t, out, `msg="error: open ./https-cert-does-not-exist: no such file or directory"`, "tls cert error does not contain expected message")
			},
		},
		"log format error": {
			args:     []string{"-log-format", "invalid"},
			wantCode: 2,
			wantOut:  "error: invalid log format \"invalid\", must be \"text\" or \"json\"\n\n" + usage,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.getHostname == nil {
				tc.getHostname = os.Hostname
			}

			buf := &bytes.Buffer{}
			gotCode := mainImpl(tc.args, func(key string) string { return tc.env[key] }, func() []string { return environSlice(tc.env) }, tc.getHostname, buf)
			out := buf.String()

			if gotCode != tc.wantCode {
				t.Logf("unexpected error: output:\n%s", out)
				t.Fatalf("expected return code %d, got %d", tc.wantCode, gotCode)
			}

			if tc.wantOutFn != nil {
				tc.wantOutFn(t, out)
				return
			}

			if out != tc.wantOut {
				t.Fatalf("output mismatch error:\nwant: %q\ngot:  %q", tc.wantOut, out)
			}
		})
	}
}

func environSlice(env map[string]string) []string {
	envStrings := make([]string, 0, len(env))
	for name, value := range env {
		envStrings = append(envStrings, fmt.Sprintf("%s=%s", name, value))
	}
	return envStrings
}

// mergedConfig takes two config struct pointers and returns a new config where
// non-zero values from the second config override values in the first config.
func mergedConfig(base, override *config) *config {
	result := &config{}
	*result = *base

	overrideVal := reflect.ValueOf(*override)
	resultVal := reflect.ValueOf(result).Elem()
	configType := overrideVal.Type()

	for i := 0; i < configType.NumField(); i++ {
		field := configType.Field(i)
		fieldName := field.Name
		overrideField := overrideVal.FieldByName(fieldName)
		resultField := resultVal.FieldByName(fieldName)
		switch field.Type.Kind() {
		case reflect.Map:
			if !overrideField.IsNil() && overrideField.Len() > 0 {
				newMap := reflect.MakeMap(field.Type)
				iter := overrideField.MapRange()
				for iter.Next() {
					newMap.SetMapIndex(iter.Key(), iter.Value())
				}
				resultField.Set(newMap)
			}
		case reflect.Slice:
			if !overrideField.IsNil() && overrideField.Len() > 0 {
				newSlice := reflect.MakeSlice(field.Type, overrideField.Len(), overrideField.Len())
				reflect.Copy(newSlice, overrideField)
				resultField.Set(newSlice)
			}
		case reflect.String:
			if overrideField.String() != "" {
				resultField.SetString(overrideField.String())
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if overrideField.Int() != 0 {
				resultField.SetInt(overrideField.Int())
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if overrideField.Uint() != 0 {
				resultField.SetUint(overrideField.Uint())
			}
		case reflect.Float32, reflect.Float64:
			if overrideField.Float() != 0 {
				resultField.SetFloat(overrideField.Float())
			}
		case reflect.Bool:
			if overrideField.Bool() {
				resultField.SetBool(overrideField.Bool())
			}
		case reflect.Pointer:
			if !overrideField.IsNil() {
				resultField.Set(overrideField)
			}
		}
	}

	return result
}
