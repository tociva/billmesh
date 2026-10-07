package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ClientType is the fixed Billmesh API surface assigned to an OAuth client.
// It is derived from trusted server configuration, never from token input.
type ClientType string

const (
	ClientCatalogue ClientType = "catalogue"
	ClientBilling   ClientType = "billing"
	ClientRuntime   ClientType = "runtime"
	ClientAdmin     ClientType = "admin"
	ClientConsole   ClientType = "console"
)

type ClientRegistration struct {
	ClientID    string     `json:"client_id"`
	Type        ClientType `json:"type"`
	App         string     `json:"app"`
	Environment string     `json:"environment"`
}

type ClientRegistry map[string]ClientRegistration

func ParseClientRegistry(raw string) (ClientRegistry, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("OIDC_CLIENT_PROFILES is required")
	}
	var registrations []ClientRegistration
	if err := json.Unmarshal([]byte(raw), &registrations); err != nil {
		return nil, fmt.Errorf("OIDC_CLIENT_PROFILES must be a JSON array: %w", err)
	}
	if len(registrations) == 0 {
		return nil, errors.New("OIDC_CLIENT_PROFILES must contain at least one client")
	}
	registry := make(ClientRegistry, len(registrations))
	for index, registration := range registrations {
		registration.ClientID = strings.TrimSpace(registration.ClientID)
		registration.App = strings.TrimSpace(registration.App)
		registration.Environment = strings.TrimSpace(registration.Environment)
		if registration.ClientID == "" || registration.App == "" || registration.Environment == "" {
			return nil, fmt.Errorf("OIDC_CLIENT_PROFILES entry %d requires client_id, app, and environment", index)
		}
		switch registration.Type {
		case ClientCatalogue, ClientBilling, ClientRuntime, ClientAdmin:
		default:
			return nil, fmt.Errorf("OIDC_CLIENT_PROFILES entry %d has unsupported type %q", index, registration.Type)
		}
		if (registration.App == "*" || registration.Environment == "*") && registration.Type != ClientAdmin {
			return nil, fmt.Errorf("OIDC_CLIENT_PROFILES entry %d uses a wildcard outside the admin profile", index)
		}
		if _, exists := registry[registration.ClientID]; exists {
			return nil, fmt.Errorf("OIDC_CLIENT_PROFILES contains duplicate client_id %q", registration.ClientID)
		}
		registry[registration.ClientID] = registration
	}
	return registry, nil
}

func (r ClientRegistry) authenticate(claims *Claims) error {
	if claims.ClientID == "" {
		return errors.New("missing client_id")
	}
	registration, ok := r[claims.ClientID]
	if !ok {
		return errors.New("unrecognized client_id")
	}
	if (registration.App != "*" && claims.App != registration.App) ||
		(registration.Environment != "*" && claims.Environment != registration.Environment) {
		return errors.New("client application context mismatch")
	}
	claims.ClientType = registration.Type
	return nil
}

func (c *Claims) IsClient(types ...ClientType) bool {
	for _, clientType := range types {
		if c.ClientType == clientType {
			return true
		}
	}
	return false
}

func (c *Claims) CanLinkProducts() bool {
	return c.IsClient(ClientAdmin)
}
