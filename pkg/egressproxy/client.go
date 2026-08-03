// Package egressproxy builds the explicit HTTP client used by VendorService
// adapters. It never performs TLS interception: the proxy sees CONNECT and the
// vendor certificate is verified by the client transport.
package egressproxy

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrRequired = errors.New("egress proxy is required")

// NewClient returns an HTTP client whose transport is explicitly bound to the
// supplied forward proxy. When required is true, an empty or malformed URL is
// a configuration error; callers must not fall back to http.DefaultClient.
func NewClient(rawURL string, required bool, timeout time.Duration) (*http.Client, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		if required {
			return nil, ErrRequired
		}
		return &http.Client{Timeout: timeout}, nil
	}
	proxyURL, err := url.Parse(rawURL)
	if err != nil || proxyURL.Host == "" || (proxyURL.Scheme != "http" && proxyURL.Scheme != "https") {
		return nil, fmt.Errorf("invalid proxy URL: %q", rawURL)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}
