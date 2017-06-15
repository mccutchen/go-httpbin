package digest

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

// Well-formed examples from Wikipedia:
// https://en.wikipedia.org/wiki/Digest_access_authentication#Example_with_explanation
const (
	exampleUsername = "Mufasa"
	examplePassword = "Circle Of Life"

	exampleChallenge string = `Digest realm="testrealm@host.com",
            qop="auth,auth-int",
            nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093",
            opaque="5ccc069c403ebaf9f0171e9517f40e41"`

	exampleAuthorization string = `Digest username="Mufasa",
			realm="testrealm@host.com",
			nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093",
			uri="/dir/index.html",
			qop=auth,
			nc=00000001,
			cnonce="0a4f113b",
			response="6629fae49393a05397450978507c4ef1",
			opaque="5ccc069c403ebaf9f0171e9517f40e41"`
)

func assertStringEquals(t *testing.T, expected, got string) {
	if expected != got {
		t.Errorf("Expected %#v, got %#v", expected, got)
	}
}

func buildRequest(method, uri, authHeader string) *http.Request {
	req, _ := http.NewRequest(method, uri, nil)
	req.RequestURI = uri
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return req
}

func TestCheck(t *testing.T) {
	t.Run("missing authorization", func(t *testing.T) {
		req := buildRequest("GET", "/dir/index.html", "")
		if Check(req, exampleUsername, examplePassword) != false {
			t.Error("Missing Authorization header should fail")
		}
	})

	t.Run("wrong username", func(t *testing.T) {
		req := buildRequest("GET", "/dir/index.html", exampleAuthorization)
		if Check(req, "Simba", examplePassword) != false {
			t.Error("Incorrect username should fail")
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		req := buildRequest("GET", "/dir/index.html", exampleAuthorization)
		if Check(req, examplePassword, "foobar") != false {
			t.Error("Incorrect password should fail")
		}
	})

	t.Run("ok", func(t *testing.T) {
		req := buildRequest("GET", "/dir/index.html", exampleAuthorization)
		if Check(req, exampleUsername, examplePassword) != true {
			t.Error("Correct credentials should pass")
		}
	})
}

func TestChallenge(t *testing.T) {
	var tests = []struct {
		realm             string
		expectedRealm     string
		algorithm         digestAlgorithm
		expectedAlgorithm string
	}{
		{"realm", "realm", MD5, "MD5"},
		{"realm", "realm", SHA256, "SHA-256"},
		{"realm with spaces", "realm with spaces", SHA256, "SHA-256"},
		{`realm "with" "quotes"`, "realm with quotes", MD5, "MD5"},
		{`spaces, "quotes," and commas`, "spaces quotes and commas", MD5, "MD5"},
	}
	for _, test := range tests {
		challenge := Challenge(test.realm, test.algorithm)
		result := parseDictHeader(challenge)
		assertStringEquals(t, test.expectedRealm, result["realm"])
		assertStringEquals(t, test.expectedAlgorithm, result["algorithm"])
	}
}

func TestResponse(t *testing.T) {
	auth := parseAuthorizationHeader(exampleAuthorization)
	expected := auth.response
	got := response(auth, examplePassword, "GET", "/dir/index.html")
	assertStringEquals(t, expected, got)
}

func TestHash(t *testing.T) {
	var tests = []struct {
		algorithm digestAlgorithm
		data      []byte
		expected  string
	}{
		{SHA256, []byte("hello, world!\n"), "4dca0fd5f424a31b03ab807cbae77eb32bf2d089eed1cee154b3afed458de0dc"},
		{MD5, []byte("hello, world!\n"), "910c8bc73110b0cd1bc5d2bcae782511"},

		// Any unhandled hash results in MD5 being used
		{digestAlgorithm(10), []byte("hello, world!\n"), "910c8bc73110b0cd1bc5d2bcae782511"},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("hash/%v", test.algorithm), func(t *testing.T) {
			result := hash(test.data, test.algorithm)
			assertStringEquals(t, test.expected, result)
		})
	}
}

func TestCompare(t *testing.T) {
	if compare("foo", "bar") != false {
		t.Error("Expected foo != bar")
	}

	if compare("foo", "foo") != true {
		t.Error("Expected foo == foo")
	}
}

func TestParseDictHeader(t *testing.T) {
	var tests = []struct {
		input    string
		expected map[string]string
	}{
		{"foo=bar", map[string]string{"foo": "bar"}},

		// keys without values get the empty string
		{"foo", map[string]string{"foo": ""}},
		{"foo=bar, baz", map[string]string{"foo": "bar", "baz": ""}},

		// no spaces required
		{"foo=bar,baz=quux", map[string]string{"foo": "bar", "baz": "quux"}},

		// spaces are stripped
		{"foo=bar, baz=quux", map[string]string{"foo": "bar", "baz": "quux"}},
		{"foo= bar, baz=quux", map[string]string{"foo": "bar", "baz": "quux"}},
		{"foo=bar, baz = quux", map[string]string{"foo": "bar", "baz": "quux"}},
		{" foo =bar, baz=quux", map[string]string{"foo": "bar", "baz": "quux"}},
		{"foo=bar,baz = quux ", map[string]string{"foo": "bar", "baz": "quux"}},

		// quotes around values are stripped
		{`foo="bar two three four", baz=quux`, map[string]string{"foo": "bar two three four", "baz": "quux"}},
		{`foo=bar, baz=""`, map[string]string{"foo": "bar", "baz": ""}},

		// quotes around keys are not stripped
		{`"foo"="bar", "baz two"=quux`, map[string]string{`"foo"`: "bar", `"baz two"`: "quux"}},

		// spaces within quotes around values are preserved
		{`foo=bar, baz=" quux "`, map[string]string{"foo": "bar", "baz": " quux "}},

		// commas values are NOT handled correctly
		{`foo="one, two, three", baz=quux`, map[string]string{"foo": `"one`, "two": "", `three"`: "", "baz": "quux"}},
		{",,,", make(map[string]string)},

		// trailing comma is okay
		{"foo=bar,", map[string]string{"foo": "bar"}},
		{"foo=bar,   ", map[string]string{"foo": "bar"}},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			results := parseDictHeader(test.input)
			if !reflect.DeepEqual(test.expected, results) {
				t.Errorf("expected %#v, got %#v", test.expected, results)
			}
		})
	}
}

func TestParseAuthorizationHeader(t *testing.T) {
	var tests = []struct {
		input    string
		expected *authorization
	}{
		{"", nil},
		{"Digest", nil},
		{"Basic QWxhZGRpbjpPcGVuU2VzYW1l", nil},

		// case sensitive on Digest
		{"digest username=u, realm=r, nonce=n", nil},

		// incomplete headers are fine
		{"Digest username=u, realm=r, nonce=n", &authorization{
			algorithm: MD5,
			username:  "u",
			realm:     "r",
			nonce:     "n",
		}},

		// algorithm can be either MD5 or SHA-256, with MD5 as default
		{"Digest username=u", &authorization{
			algorithm: MD5,
			username:  "u",
		}},
		{"Digest algorithm=MD5, username=u", &authorization{
			algorithm: MD5,
			username:  "u",
		}},
		{"Digest algorithm=md5, username=u", &authorization{
			algorithm: MD5,
			username:  "u",
		}},
		{"Digest algorithm=SHA-256, username=u", &authorization{
			algorithm: SHA256,
			username:  "u",
		}},
		{"Digest algorithm=foo, username=u", &authorization{
			algorithm: MD5,
			username:  "u",
		}},
		{"Digest algorithm=SHA-512, username=u", &authorization{
			algorithm: MD5,
			username:  "u",
		}},
		// algorithm not case sensitive
		{"Digest algorithm=sha-256, username=u", &authorization{
			algorithm: SHA256,
			username:  "u",
		}},
		// but dash is required in SHA-256 is not recognized
		{"Digest algorithm=SHA256, username=u", &authorization{
			algorithm: MD5,
			username:  "u",
		}},
		// session variants not recognized
		{"Digest algorithm=SHA-256-sess, username=u", &authorization{
			algorithm: MD5,
			username:  "u",
		}},
		{"Digest algorithm=MD5-sess, username=u", &authorization{
			algorithm: MD5,
			username:  "u",
		}},

		{exampleAuthorization, &authorization{
			algorithm: MD5,
			cnonce:    "0a4f113b",
			nc:        "00000001",
			nonce:     "dcd98b7102dd2f0e8b11d0f600bfb0c093",
			opaque:    "5ccc069c403ebaf9f0171e9517f40e41",
			qop:       "auth",
			realm:     "testrealm@host.com",
			response:  "6629fae49393a05397450978507c4ef1",
			uri:       "/dir/index.html",
			username:  exampleUsername,
		}},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			got := parseAuthorizationHeader(test.input)
			if !reflect.DeepEqual(test.expected, got) {
				t.Errorf("expected %#v, got %#v", test.expected, got)
			}
		})
	}
}
