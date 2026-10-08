package auth

import (
	"errors"
	"fmt"
	"strings"
)

// DelegationClientRegistration binds the two authenticated participants in an
// IdNest delegation to the only Billmesh authority they may exercise.
type DelegationClientRegistration struct {
	AuthorizerClientID    string     `json:"authorizer_client_id"`
	ActorClientID         string     `json:"actor_client_id"`
	Scope                 string     `json:"scope"`
	Type                  ClientType `json:"type"`
	ActorType             string     `json:"actor_type"`
	App                   string     `json:"app"`
	Environment           string     `json:"environment"`
	ContextProfileVersion int        `json:"context_profile_version"`
}

type DelegationClientRegistry map[string]DelegationClientRegistration

type DelegationClientResolver interface {
	Authenticate(*Claims, string, int) error
}

var ErrDelegationPolicyUnavailable = errors.New("delegation authorization policy is unavailable")

func delegationClientKey(authorizerClientID, actorClientID string) string {
	return authorizerClientID + "\x00" + actorClientID
}

func NewDelegationClientRegistry(registrations []DelegationClientRegistration) (DelegationClientRegistry, error) {
	if len(registrations) == 0 {
		return nil, errors.New("delegation client profiles must contain at least one client pair")
	}
	registry := make(DelegationClientRegistry, len(registrations))
	for index, registration := range registrations {
		registration.AuthorizerClientID = strings.TrimSpace(registration.AuthorizerClientID)
		registration.ActorClientID = strings.TrimSpace(registration.ActorClientID)
		registration.Scope = strings.TrimSpace(registration.Scope)
		registration.App = strings.TrimSpace(registration.App)
		registration.Environment = strings.TrimSpace(registration.Environment)
		if registration.AuthorizerClientID == "" || registration.ActorClientID == "" || registration.Scope == "" || registration.App == "" || registration.Environment == "" {
			return nil, fmt.Errorf("delegation client profile entry %d requires authorizer_client_id, actor_client_id, scope, app, and environment", index)
		}
		if strings.ContainsRune(registration.AuthorizerClientID, '\x00') || strings.ContainsRune(registration.ActorClientID, '\x00') {
			return nil, fmt.Errorf("delegation client profile entry %d contains an invalid client identifier", index)
		}
		switch registration.Type {
		case ClientCatalogue, ClientBilling, ClientRuntime, ClientAdmin:
		default:
			return nil, fmt.Errorf("delegation client profile entry %d has unsupported type %q", index, registration.Type)
		}
		if registration.ActorType != "user" && registration.ActorType != "service" {
			return nil, fmt.Errorf("delegation client profile entry %d actor_type must be user or service", index)
		}
		expectedScope := map[ClientType]string{
			ClientCatalogue: "billmesh.catalogue",
			ClientBilling:   "billmesh.billing",
			ClientRuntime:   "billmesh.runtime",
			ClientAdmin:     "billmesh.admin",
		}[registration.Type]
		if registration.Scope != expectedScope {
			return nil, fmt.Errorf("delegation client profile entry %d scope must be %q for type %q", index, expectedScope, registration.Type)
		}
		if (registration.Type == ClientCatalogue || registration.Type == ClientRuntime) && registration.ActorType != "service" {
			return nil, fmt.Errorf("delegation client profile entry %d type %q requires a service actor", index, registration.Type)
		}
		if (registration.App == "*" || registration.Environment == "*") && registration.Type != ClientAdmin {
			return nil, fmt.Errorf("delegation client profile entry %d uses a wildcard outside the admin profile", index)
		}
		if (registration.App == "*") != (registration.Environment == "*") {
			return nil, fmt.Errorf("delegation client profile entry %d must wildcard application and environment together", index)
		}
		if registration.ContextProfileVersion != 1 {
			return nil, fmt.Errorf("delegation client profile entry %d has unsupported context_profile_version %d", index, registration.ContextProfileVersion)
		}
		key := delegationClientKey(registration.AuthorizerClientID, registration.ActorClientID)
		if _, exists := registry[key]; exists {
			return nil, fmt.Errorf("delegation client profiles contain duplicate authorizer/actor pair %q/%q", registration.AuthorizerClientID, registration.ActorClientID)
		}
		registry[key] = registration
	}
	return registry, nil
}

func (r DelegationClientRegistry) Authenticate(claims *Claims, scope string, contextProfileVersion int) error {
	registration, ok := r[delegationClientKey(claims.AuthorizerClientID, claims.ClientID)]
	if !ok {
		return errors.New("unrecognized delegation authorizer/actor pair")
	}
	if scope != registration.Scope {
		return errors.New("delegated token scope does not match the registered actor profile")
	}
	if contextProfileVersion != registration.ContextProfileVersion {
		return errors.New("delegated token context profile does not match the registered actor profile")
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
