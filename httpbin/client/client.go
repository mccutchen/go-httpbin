package client

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type Client interface {
	Get(headers, query map[string][]string) *http.Request
	Post(headers, query map[string][]string, body io.ReadCloser) *http.Request

	Do(*http.Request) (*http.Response, error)
}

type client struct {
	client  *http.Client
	address string
}

func NewClient(cl *http.Client, address string) *client {
	return &client{
		client:  cl,
		address: address,
	}
}

func (c *client) baseRequest(uri string, headers, query map[string][]string) *http.Request {
	u, err := url.Parse(fmt.Sprintf("%s/%s", c.address, uri))
	if err != nil {
		fmt.Printf("Unable to parse url for get: %e", err)
		return nil
	}

	if query != nil {
		u.RawQuery = url.Values(query).Encode()
	}
	return &http.Request{
		Header: headers,
		URL:    u,
	}
}

func (c *client) Get(headers, query map[string][]string) *http.Request {
	req := c.baseRequest("get", headers, query)
	req.Method = http.MethodGet
	return req
}

func (c *client) Post(headers, query map[string][]string, body io.ReadCloser) *http.Request {
	req := c.baseRequest("post", headers, query)
	req.Method = http.MethodPost
	req.Body = body
	return req
}

func (c *client) Do(req *http.Request) (*http.Response, error) {
	return c.client.Do(req)
}
