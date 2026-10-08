package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DelegationClientRegistration binds the two authenticated participants in an
// IdNest delegation to the only Billmesh authority they may exercise.
type DelegationClientRegistration struct {
	AuthorizerClientID string     `json:"authorizer_client_id"`
	ActorClientID      string     `json:"actor_client_id"`
	Scope              string     `json:"scope"`
	Type               ClientType `json:"type"`
	ActorType          string     `json:"actor_type"`
	App                string     `json:"app"`
	Environment        string     `json:"environment"`
}

type DelegationClientRegistry map[string]DelegationClientRegistration

func delegationClientKey(authorizerClientID, actorClientID string) string {
	return authorizerClientID + "\x00" + actorClientID
}

func ParseDelegationClientRegistry(raw string) (DelegationClientRegistry, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("DELEGATION_CLIENT_PROFILES is required")
	}
	var registrations []DelegationClientRegistration
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&registrations); err != nil {
		return nil, fmt.Errorf("DELEGATION_CLIENT_PROFILES must be a JSON array: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("DELEGATION_CLIENT_PROFILES must contain exactly one JSON array")
	}
	if len(registrations) == 0 {
		return nil, errors.New("DELEGATION_CLIENT_PROFILES must contain at least one client pair")
	}
	registry := make(DelegationClientRegistry, len(registrations))
	for index, registration := range registrations {
		registration.AuthorizerClientID = strings.TrimSpace(registration.AuthorizerClientID)
		registration.ActorClientID = strings.TrimSpace(registration.ActorClientID)
		registration.Scope = strings.TrimSpace(registration.Scope)
		registration.App = strings.TrimSpace(registration.App)
		registration.Environment = strings.TrimSpace(registration.Environment)
		if registration.AuthorizerClientID == "" || registration.ActorClientID == "" || registration.Scope == "" || registration.App == "" || registration.Environment == "" {
			return nil, fmt.Errorf("DELEGATION_CLIENT_PROFILES entry %d requires authorizer_client_id, actor_client_id, scope, app, and environment", index)
		}
		if strings.ContainsRune(registration.AuthorizerClientID, '\x00') || strings.ContainsRune(registration.ActorClientID, '\x00') {
			return nil, fmt.Errorf("DELEGATION_CLIENT_PROFILES entry %d contains an invalid client identifier", index)
		}
		switch registration.Type {
		case ClientCatalogue, ClientBilling, ClientRuntime, ClientAdmin:
		default:
			return nil, fmt.Errorf("DELEGATION_CLIENT_PROFILES entry %d has unsupported type %q", index, registration.Type)
		}
		if registration.ActorType != "user" && registration.ActorType != "service" {
			return nil, fmt.Errorf("DELEGATION_CLIENT_PROFILES entry %d actor_type must be user or service", index)
		}
		expectedScope := map[ClientType]string{
			ClientCatalogue: "billmesh.catalogue",
			ClientBilling:   "billmesh.billing",
			ClientRuntime:   "billmesh.runtime",
			ClientAdmin:     "billmesh.admin",
		}[registration.Type]
		if registration.Scope != expectedScope {
			return nil, fmt.Errorf("DELEGATION_CLIENT_PROFILES entry %d scope must be %q for type %q", index, expectedScope, registration.Type)
		}
		if (registration.Type == ClientCatalogue || registration.Type == ClientRuntime) && registration.ActorType != "service" {
			return nil, fmt.Errorf("DELEGATION_CLIENT_PROFILES entry %d type %q requires a service actor", index, registration.Type)
		}
		if (registration.App == "*" || registration.Environment == "*") && registration.Type != ClientAdmin {
			return nil, fmt.Errorf("DELEGATION_CLIENT_PROFILES entry %d uses a wildcard outside the admin profile", index)
		}
		if (registration.App == "*") != (registration.Environment == "*") {
			return nil, fmt.Errorf("DELEGATION_CLIENT_PROFILES entry %d must wildcard application and environment together", index)
		}
		key := delegationClientKey(registration.AuthorizerClientID, registration.ActorClientID)
		if _, exists := registry[key]; exists {
			return nil, fmt.Errorf("DELEGATION_CLIENT_PROFILES contains duplicate authorizer/actor pair %q/%q", registration.AuthorizerClientID, registration.ActorClientID)
		}
		registry[key] = registration
	}
	return registry, nil
}

func (r DelegationClientRegistry) authenticate(claims *Claims, scope string) error {
	registration, ok := r[delegationClientKey(claims.AuthorizerClientID, claims.ClientID)]
	if !ok {
		return errors.New("unrecognized delegation authorizer/actor pair")
	}
	if scope != registration.Scope {
		return errors.New("delegated token scope does not match the registered actor profile")
	}
	if registration.App == "*" {
		if claims.App == "" {
			return errors.New("delegated administrator token is missing application context")
		}
	} else {
		if claims.App != "" {
			return errors.New("delegated token supplies application context for a fixed registration")
		}
		claims.App = registration.App
	}
	if registration.Environment == "*" {
		if claims.Environment == "" {
			return errors.New("delegated administrator token is missing environment context")
		}
	} else {
		if claims.Environment != "" {
			return errors.New("delegated token supplies environment context for a fixed registration")
		}
		claims.Environment = registration.Environment
	}
	claims.ClientType = registration.Type
	claims.ActorType = registration.ActorType
	return nil
}
