//go:build e2e

package foundation_test

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHealthAndReadiness(t *testing.T) {
	base := os.Getenv("BILLMESH_BASE_URL")
	require.NotEmpty(t, base)
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(base + path)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var body struct {
			Status       string            `json:"status"`
			Capabilities map[string]string `json:"capabilities"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		resp.Body.Close()
		if path == "/healthz" {
			require.Equal(t, "ok", body.Status)
			require.Empty(t, body.Capabilities)
			continue
		}
		require.Equal(t, "ready", body.Status)
		require.Equal(t, "enabled", body.Capabilities["razorpay_checkout"])
	}
}
