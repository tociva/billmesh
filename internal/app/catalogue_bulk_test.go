package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tociva/billmesh/internal/products"
)

func validBulkDocument() catalogueBulkDocument {
	return catalogueBulkDocument{
		SchemaVersion: catalogueBulkSchemaVersion,
		Products: []catalogueBulkProduct{{
			Operation:         "create",
			ExpectedVersion:   0,
			Slug:              "daybook",
			Name:              "Daybook",
			Description:       "Daybook billing catalogue",
			EntitlementSchema: products.EntitlementSchema{Fields: []products.EntitlementField{}},
			BillingPolicy:     products.DefaultBillingPolicy(),
			Active:            boolValue(true),
			DelegatedAccess: []productDelegationConfigurationInput{{
				Environment:        "development",
				ExpectedVersion:    0,
				Enabled:            true,
				AuthorizerClientID: "daybook-billmesh-authorizer-dev",
				Actors: []productDelegationActorInput{
					{ClientID: "daybook-billmesh-user-dev", Profile: "billing", ActorType: "user", Enabled: true},
					{ClientID: "daybook-billmesh-billing-dev", Profile: "billing", ActorType: "service", Enabled: true},
					{ClientID: "daybook-billmesh-runtime-dev", Profile: "runtime", ActorType: "service", Enabled: true},
					{ClientID: "daybook-billmesh-catalogue-dev", Profile: "catalogue", ActorType: "service", Enabled: true},
				},
			}},
			Plans: []catalogueBulkPlan{{
				Operation:       "create",
				ExpectedVersion: 0,
				catalogueTransferPlan: catalogueTransferPlan{
					Slug: "free", PlanFamilyID: "free", Name: "Free", PriceMinor: 0, Currency: "INR",
					Entitlements: map[string]any{}, BillingInterval: "monthly", BillingModel: "free",
					Selectable: boolValue(true), Default: boolValue(true), CheckoutEnabled: boolValue(true), Active: boolValue(true),
				},
			}},
		}},
	}
}

func TestValidateCatalogueBulkDocumentAcceptsVersionedCreate(t *testing.T) {
	validation := validateCatalogueBulkDocument(validBulkDocument(), time.Now().UTC())
	require.True(t, validation.Valid, validation.Issues)
	require.Empty(t, validation.Issues)
	require.Equal(t, 1, validation.Products)
	require.Equal(t, 1, validation.Plans)
}

func TestValidateCatalogueBulkDocumentAcceptsProductSpecificActorSet(t *testing.T) {
	document := validBulkDocument()
	document.Products[0].DelegatedAccess[0].Actors = []productDelegationActorInput{
		{ClientID: "daybook-billmesh-catalogue-dev", Profile: "catalogue", ActorType: "service", Enabled: true},
	}

	validation := validateCatalogueBulkDocument(document, time.Now().UTC())
	require.True(t, validation.Valid, validation.Issues)
}

func TestValidateCatalogueBulkDocumentRejectsUnsupportedActorCombination(t *testing.T) {
	document := validBulkDocument()
	document.Products[0].DelegatedAccess[0].Actors = []productDelegationActorInput{
		{ClientID: "daybook-billmesh-catalogue-dev", Profile: "catalogue", ActorType: "user", Enabled: true},
	}

	validation := validateCatalogueBulkDocument(document, time.Now().UTC())
	require.False(t, validation.Valid)
	require.Contains(t, validation.Issues[0].Message, "service")
}

func TestValidateCatalogueBulkDocumentRejectsUnsafeOperations(t *testing.T) {
	document := validBulkDocument()
	document.Products[0].Operation = "update"
	document.Products[0].ExpectedVersion = 0
	document.Products[0].Plans[0].Operation = "delete"
	document.Products[0].DelegatedAccess[0].Actors[1].ClientID = document.Products[0].DelegatedAccess[0].Actors[0].ClientID

	validation := validateCatalogueBulkDocument(document, time.Now().UTC())
	require.False(t, validation.Valid)
	require.NotEmpty(t, validation.Issues)
}

func TestCatalogueBulkJSONFlattensPlanFields(t *testing.T) {
	raw := []byte(`{
		"schema_version":1,
		"products":[{
			"operation":"create","expected_version":0,"slug":"daybook","name":"Daybook","description":"",
			"entitlement_schema":{"fields":[]},"billing_policy":` + mustJSON(t, products.DefaultBillingPolicy()) + `,"active":true,
			"plans":[{"operation":"create","expected_version":0,"slug":"free","plan_family_id":"free","name":"Free","description":"","price_minor":0,"currency":"INR","included_credits":0,"entitlements":{},"billing_interval":"monthly","billing_model":"free","selectable":true,"default_for_product":true,"checkout_enabled":true,"active":true}]
		}]
	}`)
	var document catalogueBulkDocument
	require.NoError(t, json.Unmarshal(raw, &document))
	require.Equal(t, "free", document.Products[0].Plans[0].Slug)
	require.Equal(t, "create", document.Products[0].Plans[0].Operation)
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return string(raw)
}
