package delegationprofiles

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tociva/billmesh/internal/auth"
)

type Loader interface {
	Load(context.Context) ([]auth.DelegationClientRegistration, error)
}

type PostgreSQLLoader struct {
	pool *pgxpool.Pool
}

func NewPostgreSQLLoader(pool *pgxpool.Pool) *PostgreSQLLoader {
	return &PostgreSQLLoader{pool: pool}
}

func (l *PostgreSQLLoader) Load(ctx context.Context) ([]auth.DelegationClientRegistration, error) {
	rows, err := l.pool.Query(ctx, `SELECT authorizer_client_id, actor_client_id, scope, client_type,
		actor_type, application, environment, context_profile_version
		FROM delegation_client_profiles WHERE enabled ORDER BY authorizer_client_id, actor_client_id`)
	if err != nil {
		return nil, fmt.Errorf("query delegation client profiles: %w", err)
	}
	defer rows.Close()

	registrations := make([]auth.DelegationClientRegistration, 0)
	for rows.Next() {
		var registration auth.DelegationClientRegistration
		var clientType string
		if err := rows.Scan(
			&registration.AuthorizerClientID,
			&registration.ActorClientID,
			&registration.Scope,
			&clientType,
			&registration.ActorType,
			&registration.App,
			&registration.Environment,
			&registration.ContextProfileVersion,
		); err != nil {
			return nil, fmt.Errorf("scan delegation client profile: %w", err)
		}
		registration.Type = auth.ClientType(clientType)
		registrations = append(registrations, registration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read delegation client profiles: %w", err)
	}
	return registrations, nil
}

type registrySnapshot struct {
	registry auth.DelegationClientRegistry
	loadedAt time.Time
}

type CachedResolver struct {
	loader          Loader
	refreshInterval time.Duration
	maxStaleness    time.Duration
	now             func() time.Time
	snapshot        atomic.Pointer[registrySnapshot]
}

func NewCachedResolver(loader Loader, refreshInterval, maxStaleness time.Duration) (*CachedResolver, error) {
	if loader == nil {
		return nil, errors.New("delegation profile loader is required")
	}
	if refreshInterval <= 0 || maxStaleness <= 0 {
		return nil, errors.New("delegation profile refresh and staleness durations must be positive")
	}
	if refreshInterval >= maxStaleness {
		return nil, errors.New("delegation profile refresh interval must be shorter than maximum staleness")
	}
	return &CachedResolver{
		loader: loader, refreshInterval: refreshInterval, maxStaleness: maxStaleness, now: time.Now,
	}, nil
}

func (r *CachedResolver) Refresh(ctx context.Context) error {
	registrations, err := r.loader.Load(ctx)
	if err != nil {
		return err
	}
	registry, err := auth.NewDelegationClientRegistry(registrations)
	if err != nil {
		return fmt.Errorf("validate delegation client profiles: %w", err)
	}
	r.snapshot.Store(&registrySnapshot{registry: registry, loadedAt: r.now()})
	return nil
}

func (r *CachedResolver) Run(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(r.refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.Refresh(ctx); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}

func (r *CachedResolver) Authenticate(claims *auth.Claims, scope string, contextProfileVersion int) error {
	snapshot := r.snapshot.Load()
	if snapshot == nil || r.now().Sub(snapshot.loadedAt) > r.maxStaleness {
		return auth.ErrDelegationPolicyUnavailable
	}
	return snapshot.registry.Authenticate(claims, scope, contextProfileVersion)
}
