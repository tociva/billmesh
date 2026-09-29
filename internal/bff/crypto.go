package bff

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type keyring struct {
	primary string
	keys    map[string][]byte
}

type envelope struct {
	KeyID      string `json:"kid"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func newKeyring(raw string) (*keyring, error) {
	ring := &keyring{keys: make(map[string][]byte)}
	for _, entry := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), ":", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil, errors.New("BFF_SESSION_ENCRYPTION_KEYS must contain id:base64url-key entries")
		}
		key, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("BFF session key %q must be 32 base64url-encoded bytes", parts[0])
		}
		if _, exists := ring.keys[parts[0]]; exists {
			return nil, fmt.Errorf("duplicate BFF session key id %q", parts[0])
		}
		if ring.primary == "" {
			ring.primary = parts[0]
		}
		ring.keys[parts[0]] = key
	}
	if ring.primary == "" {
		return nil, errors.New("at least one BFF session encryption key is required")
	}
	return ring, nil
}

func (k *keyring) encrypt(purpose string, value any) ([]byte, error) {
	plaintext, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k.keys[k.primary])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, plaintext, []byte(purpose))
	return json.Marshal(envelope{
		KeyID: k.primary, Nonce: base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawURLEncoding.EncodeToString(sealed),
	})
}

func (k *keyring) decrypt(purpose string, raw []byte, target any) error {
	var wrapped envelope
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return errors.New("invalid encrypted session envelope")
	}
	key, ok := k.keys[wrapped.KeyID]
	if !ok {
		return errors.New("unknown session encryption key")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(wrapped.Nonce)
	if err != nil {
		return errors.New("invalid encrypted session nonce")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(wrapped.Ciphertext)
	if err != nil {
		return errors.New("invalid encrypted session ciphertext")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(purpose))
	if err != nil {
		return errors.New("could not decrypt session")
	}
	if err := json.Unmarshal(plaintext, target); err != nil {
		return errors.New("invalid encrypted session payload")
	}
	return nil
}

func randomIdentifier() (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func digest(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}
