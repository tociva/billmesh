package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var delegationEnvironmentPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type productDelegationActorInput struct {
	ClientID  string `json:"client_id"`
	Profile   string `json:"profile"`
	ActorType string `json:"actor_type"`
	Enabled   bool   `json:"enabled"`
}

type productDelegationConfigurationInput struct {
	Environment        string                        `json:"environment"`
	ExpectedVersion    int64                         `json:"expected_version"`
	Enabled            bool                          `json:"enabled"`
	AuthorizerClientID string                        `json:"authorizer_client_id"`
	Actors             []productDelegationActorInput `json:"actors"`
}

type productDelegationConfigurationRecord struct {
	ProductID          uuid.UUID                     `json:"product_id"`
	Environment        string                        `json:"environment"`
	AuthorizerClientID string                        `json:"authorizer_client_id"`
	Enabled            bool                          `json:"enabled"`
	Version            int64                         `json:"version"`
	Actors             []productDelegationActorInput `json:"actors"`
	CreatedAt          time.Time                     `json:"created_at"`
	UpdatedAt          time.Time                     `json:"updated_at"`
}

func normalizeAndValidateDelegationConfigurations(inputs []productDelegationConfigurationInput, create bool) ([]productDelegationConfigurationInput, error) {
	seenEnvironments := make(map[string]bool, len(inputs))
	for index := range inputs {
		inputs[index] = normalizeDelegationConfiguration(inputs[index])
		if err := validateDelegationConfiguration(inputs[index]); err != nil {
			return nil, err
		}
		if seenEnvironments[inputs[index].Environment] {
			return nil, errors.New("delegation environments must be unique")
		}
		if create && inputs[index].ExpectedVersion != 0 {
			return nil, errors.New("new delegation configurations must use expected_version 0")
		}
		seenEnvironments[inputs[index].Environment] = true
	}
	return inputs, nil
}

type delegationConfigurationQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type delegationConfigurationLister interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func normalizeDelegationConfiguration(input productDelegationConfigurationInput) productDelegationConfigurationInput {
	input.Environment = strings.TrimSpace(input.Environment)
	input.AuthorizerClientID = strings.TrimSpace(input.AuthorizerClientID)
	if input.Actors == nil {
		input.Actors = []productDelegationActorInput{}
	}
	for index := range input.Actors {
		input.Actors[index].ClientID = strings.TrimSpace(input.Actors[index].ClientID)
		input.Actors[index].Profile = strings.TrimSpace(input.Actors[index].Profile)
		input.Actors[index].ActorType = strings.TrimSpace(input.Actors[index].ActorType)
	}
	return input
}

func validateDelegationConfiguration(input productDelegationConfigurationInput) error {
	if !delegationEnvironmentPattern.MatchString(input.Environment) {
		return errors.New("environment must be a lowercase, hyphenated identifier")
	}
	if input.AuthorizerClientID == "" || len(input.AuthorizerClientID) > 255 {
		return errors.New("authorizer_client_id is required and must not exceed 255 characters")
	}
	if input.ExpectedVersion < 0 {
		return errors.New("expected_version cannot be negative")
	}
	seenClients := make(map[string]bool, len(input.Actors))
	enabledActors := 0
	for _, actor := range input.Actors {
		if actor.ClientID == "" || len(actor.ClientID) > 255 {
			return errors.New("actor client IDs are required and must not exceed 255 characters")
		}
		if actor.ClientID == input.AuthorizerClientID {
			return errors.New("the authorizer client cannot also be an actor")
		}
		if seenClients[actor.ClientID] {
			return errors.New("actor client IDs must be unique within an environment")
		}
		seenClients[actor.ClientID] = true
		switch actor.Profile {
		case "billing":
			if actor.ActorType != "user" && actor.ActorType != "service" {
				return errors.New("billing actors must have actor_type user or service")
			}
		case "catalogue", "runtime":
			if actor.ActorType != "service" {
				return errors.New(actor.Profile + " actors must have actor_type service")
			}
		default:
			return errors.New("actor profile must be catalogue, billing, or runtime")
		}
		if actor.Enabled {
			enabledActors++
		}
	}
	if input.Enabled && enabledActors == 0 {
		return errors.New("enabled delegation configurations require at least one enabled actor")
	}
	return nil
}

func configureProductDelegation(ctx context.Context, q delegationConfigurationQueryer, productID uuid.UUID, input productDelegationConfigurationInput) (productDelegationConfigurationRecord, error) {
	input = normalizeDelegationConfiguration(input)
	if err := validateDelegationConfiguration(input); err != nil {
		return productDelegationConfigurationRecord{}, err
	}
	actors, err := json.Marshal(input.Actors)
	if err != nil {
		return productDelegationConfigurationRecord{}, err
	}
	var version int64
	if err := q.QueryRow(ctx, `SELECT configure_product_delegation($1,$2,$3,$4::jsonb,$5,$6)`,
		productID, input.Environment, input.AuthorizerClientID, actors, input.Enabled, input.ExpectedVersion).Scan(&version); err != nil {
		return productDelegationConfigurationRecord{}, err
	}
	return getProductDelegationConfiguration(ctx, q, productID, input.Environment)
}

func getProductDelegationConfiguration(ctx context.Context, q delegationConfigurationQueryer, productID uuid.UUID, environment string) (productDelegationConfigurationRecord, error) {
	var item productDelegationConfigurationRecord
	var actorsJSON []byte
	err := q.QueryRow(ctx, `SELECT configuration.product_id,configuration.environment,configuration.authorizer_client_id,
		configuration.enabled,configuration.version,configuration.created_at,configuration.updated_at,
		COALESCE((SELECT jsonb_agg(jsonb_build_object(
			'client_id',actor.actor_client_id,'profile',actor.profile,'actor_type',actor.actor_type,'enabled',actor.enabled
		) ORDER BY actor.actor_client_id) FROM product_delegation_actors actor
		WHERE actor.product_id=configuration.product_id AND actor.environment=configuration.environment),'[]'::jsonb)
		FROM product_delegation_configurations configuration
		WHERE configuration.product_id=$1 AND configuration.environment=$2`, productID, environment).
		Scan(&item.ProductID, &item.Environment, &item.AuthorizerClientID, &item.Enabled, &item.Version, &item.CreatedAt, &item.UpdatedAt, &actorsJSON)
	if err != nil {
		return item, err
	}
	if err := json.Unmarshal(actorsJSON, &item.Actors); err != nil {
		return item, err
	}
	return item, nil
}

func listProductDelegationConfigurationRecords(ctx context.Context, q delegationConfigurationLister, productID uuid.UUID) ([]productDelegationConfigurationRecord, error) {
	rows, err := q.Query(ctx, `SELECT configuration.product_id,configuration.environment,configuration.authorizer_client_id,
		configuration.enabled,configuration.version,configuration.created_at,configuration.updated_at,
		COALESCE((SELECT jsonb_agg(jsonb_build_object(
			'client_id',actor.actor_client_id,'profile',actor.profile,'actor_type',actor.actor_type,'enabled',actor.enabled
		) ORDER BY actor.actor_client_id) FROM product_delegation_actors actor
		WHERE actor.product_id=configuration.product_id AND actor.environment=configuration.environment),'[]'::jsonb)
		FROM product_delegation_configurations configuration WHERE configuration.product_id=$1 ORDER BY configuration.environment`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []productDelegationConfigurationRecord{}
	for rows.Next() {
		var item productDelegationConfigurationRecord
		var actorsJSON []byte
		if err := rows.Scan(&item.ProductID, &item.Environment, &item.AuthorizerClientID, &item.Enabled, &item.Version, &item.CreatedAt, &item.UpdatedAt, &actorsJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(actorsJSON, &item.Actors); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (a *API) listProductDelegationConfigurations(w http.ResponseWriter, r *http.Request) {
	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	var exists bool
	if err := a.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM products WHERE id=$1)`, productID).Scan(&exists); err != nil {
		writeDBError(w, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "product not found")
		return
	}
	items, err := listProductDelegationConfigurationRecords(r.Context(), a.pool, productID)
	if err != nil {
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *API) putProductDelegationConfiguration(w http.ResponseWriter, r *http.Request) {
	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	var input productDelegationConfigurationInput
	if !decode(w, r, &input) {
		return
	}
	pathEnvironment := strings.TrimSpace(r.PathValue("environment"))
	if bodyEnvironment := strings.TrimSpace(input.Environment); bodyEnvironment != "" && bodyEnvironment != pathEnvironment {
		writeError(w, http.StatusBadRequest, "body environment must match the request path")
		return
	}
	input.Environment = pathEnvironment
	input = normalizeDelegationConfiguration(input)
	if err := validateDelegationConfiguration(input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	item, err := configureProductDelegation(r.Context(), a.pool, productID, input)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "23505") {
			writeError(w, http.StatusConflict, "delegation configuration changed or a client is already assigned")
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "product not found")
			return
		}
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			writeError(w, http.StatusBadRequest, pgErr.Message)
			return
		}
		writeDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
