package app

import "crypto/hmac"

// secureHMACEqual centralizes signature comparison so webhook and browser
// checkout verification cannot regress to an ordinary, timing-sensitive
// byte or string comparison.
func secureHMACEqual(expected, provided []byte) bool {
	return hmac.Equal(expected, provided)
}
