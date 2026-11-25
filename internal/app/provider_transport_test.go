package app

import (
	"net/http"
	"testing"
	"time"
)

func TestProviderHTTPClientHasFiniteTimeoutsAndConnectionLimits(t *testing.T) {
	client := newProviderHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if client.Timeout != 5*time.Second || transport.ResponseHeaderTimeout <= 0 || transport.TLSHandshakeTimeout <= 0 || transport.IdleConnTimeout <= 0 {
		t.Fatalf("timeouts are not bounded: client=%s response=%s TLS=%s idle=%s", client.Timeout, transport.ResponseHeaderTimeout, transport.TLSHandshakeTimeout, transport.IdleConnTimeout)
	}
	if transport.MaxConnsPerHost <= 0 || transport.MaxIdleConnsPerHost <= 0 || transport.MaxIdleConns <= 0 {
		t.Fatalf("connection limits are not bounded: max=%d idle-host=%d idle=%d", transport.MaxConnsPerHost, transport.MaxIdleConnsPerHost, transport.MaxIdleConns)
	}
}
