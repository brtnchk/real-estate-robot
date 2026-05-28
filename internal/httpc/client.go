// Package httpc builds *http.Client values with sensible defaults for
// scraping workers. Factored out so fetcher and enrich don't drift apart
// on transport settings.
package httpc

import (
	"errors"
	"net"
	"net/http"
	"time"
)

// New returns a *http.Client wired with transport-level timeouts. Use
// ctx-based per-request deadlines in addition for tight control.
func New(perRequestTimeout time.Duration) *http.Client {
	if perRequestTimeout <= 0 {
		perRequestTimeout = 30 * time.Second
	}
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   10,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   perRequestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}