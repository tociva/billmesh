package bff

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func encodedKey(fill byte) string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = fill
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestKeyringEncryptsWithPrimaryAndDecryptsAfterRotation(t *testing.T) {
	oldRing, err := newKeyring("old:" + encodedKey(1))
	require.NoError(t, err)
	payload, err := oldRing.encrypt("browser-session", map[string]string{"access_token": "secret"})
	require.NoError(t, err)
	require.NotContains(t, string(payload), "secret")

	rotated, err := newKeyring("new:" + encodedKey(2) + ",old:" + encodedKey(1))
	require.NoError(t, err)
	var got map[string]string
	require.NoError(t, rotated.decrypt("browser-session", payload, &got))
	require.Equal(t, "secret", got["access_token"])
	require.Error(t, rotated.decrypt("browser-login", payload, &got), "encryption purpose must be authenticated")
}

func TestKeyringRejectsMalformedConfiguration(t *testing.T) {
	for _, value := range []string{"", "missing-separator", "v1:short", "v1:" + encodedKey(1) + ",v1:" + encodedKey(2)} {
		_, err := newKeyring(value)
		require.Error(t, err)
	}
}
