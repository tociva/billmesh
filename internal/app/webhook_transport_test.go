package app

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSecurityWebhookRejectsPrivateAddressFamilies(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "198.18.0.1", "0.0.0.0", "fc00::1", "fe80::1", "::ffff:127.0.0.1"} {
		t.Run(host, func(t *testing.T) { require.True(t, blockedWebhookIP(net.ParseIP(host))) })
	}
	for _, host := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		t.Run(host, func(t *testing.T) { require.False(t, blockedWebhookIP(net.ParseIP(host))) })
	}
}

func TestSecurityWebhookRejectsPrivateDNSAndRebindingAtDialTime(t *testing.T) {
	address := net.IPAddr{IP: net.ParseIP("93.184.216.34")}
	lookup := func(_ context.Context, _ string) ([]net.IPAddr, error) { return []net.IPAddr{address}, nil }
	require.NoError(t, validateWebhookTarget(context.Background(), "https://receiver.example/hook", lookup))
	address = net.IPAddr{IP: net.ParseIP("127.0.0.1")}
	require.Error(t, validateWebhookTarget(context.Background(), "https://receiver.example/hook", lookup))
	client := newWebhookClient(lookup, nil)
	req, err := http.NewRequest(http.MethodPost, "http://receiver.example:8080/hook", strings.NewReader("{}"))
	require.NoError(t, err)
	_, err = client.Do(req)
	require.ErrorContains(t, err, "prohibited address")
}

func TestSecurityWebhookBlocksRedirectToPrivateHost(t *testing.T) {
	t.Setenv("WEBHOOK_ALLOWED_PRIVATE_HOSTS", "127.0.0.1")
	var privateHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://private.invalid:"+r.URL.Query().Get("port")+"/private", http.StatusTemporaryRedirect)
			return
		}
		privateHits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	parsed, err := url.Parse(srv.URL)
	require.NoError(t, err)
	lookup := func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	client := newWebhookClient(lookup, nil)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/redirect?port="+parsed.Port(), strings.NewReader("{}"))
	require.NoError(t, err)
	_, err = client.Do(req)
	require.ErrorContains(t, err, "prohibited address")
	require.Zero(t, privateHits.Load())
}
