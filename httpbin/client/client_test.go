package client

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGet(t *testing.T) {
	c := client{
		address: "test",
	}

	var headers http.Header
	headers = map[string][]string{
		"header1": {"val1", "val2"},
		"header2": {"val2"},
	}
	var query url.Values
	query = map[string][]string{
		"hello": {"world"},
	}

	req := c.Get(headers, query)

	assert.NotNil(t, req, "Expected request to not be nil")
	assert.Equal(t, http.MethodGet, req.Method, "Expected GET request method")
	assert.Equal(t, headers, req.Header, "Expected request headers to be the same")
	assert.Equal(t, query, req.URL.Query(), "Expected query to match")
}

func TestPost(t *testing.T) {
	c := client{
		address: "test",
	}

	var headers http.Header
	headers = map[string][]string{
		"header1": {"val1", "val2"},
		"header2": {"val2"},
	}
	var query url.Values
	query = map[string][]string{
		"hello": {"world"},
	}

	body := io.NopCloser(strings.NewReader("data"))

	req := c.Post(headers, query, body)

	assert.NotNil(t, req, "Expected request to not be nil")
	assert.Equal(t, http.MethodPost, req.Method, "Expected GET request method")
	assert.Equal(t, headers, req.Header, "Expected request headers to be the same")
	assert.Equal(t, query, req.URL.Query(), "Expected query to match")
	assert.Equal(t, body, req.Body, "Expected request body to match")
}
