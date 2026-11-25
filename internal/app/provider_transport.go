package app

import (
	"net/http"
	"time"
)

const providerRequestTimeout = 5 * time.Second

func newProviderHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 16
	transport.MaxConnsPerHost = 32
	transport.IdleConnTimeout = 30 * time.Second
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 4 * time.Second
	transport.ExpectContinueTimeout = time.Second
	return &http.Client{Timeout: providerRequestTimeout, Transport: transport}
}
