package bff

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type sessionStore struct {
	pool      *pgxpool.Pool
	keys      *keyring
	idleTTL   time.Duration
	loginTTL  time.Duration
	logoutTTL time.Duration
	now       func() time.Time
}

func newSessionStore(pool *pgxpool.Pool, keys *keyring, idleTTL, loginTTL, logoutTTL time.Duration) *sessionStore {
	return &sessionStore{pool: pool, keys: keys, idleTTL: idleTTL, loginTTL: loginTTL, logoutTTL: logoutTTL, now: time.Now}
}

func (s *sessionStore) saveLogin(ctx context.Context, transaction loginTransaction) error {
	_, _ = s.pool.Exec(ctx, `DELETE FROM browser_login_transactions WHERE expires_at<=now()`)
	_, _ = s.pool.Exec(ctx, `DELETE FROM browser_logout_transactions WHERE expires_at<=now()`)
	_, _ = s.pool.Exec(ctx, `DELETE FROM browser_sessions WHERE idle_expires_at<=now() OR absolute_expires_at<=now()`)
	payload, err := s.keys.encrypt("browser-login", transaction)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO browser_login_transactions(id_digest,payload,expires_at)
		VALUES($1,$2,$3)`, digest(transaction.State), payload, s.now().Add(s.loginTTL))
	return err
}

func (s *sessionStore) takeLogin(ctx context.Context, state string) (*loginTransaction, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx, `DELETE FROM browser_login_transactions
		WHERE id_digest=$1 AND expires_at>now() RETURNING payload`, digest(state)).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var transaction loginTransaction
	if err := s.keys.decrypt("browser-login", payload, &transaction); err != nil {
		return nil, err
	}
	return &transaction, nil
}

func (s *sessionStore) createSession(ctx context.Context, session browserSession) error {
	payload, err := s.keys.encrypt("browser-session", session)
	if err != nil {
		return err
	}
	idleExpiry := s.now().Add(s.idleTTL)
	if session.AbsoluteExpiry.Before(idleExpiry) {
		idleExpiry = session.AbsoluteExpiry
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO browser_sessions
		(id_digest,payload,idle_expires_at,absolute_expires_at)
		VALUES($1,$2,$3,$4)`, digest(session.SessionID), payload, idleExpiry, session.AbsoluteExpiry)
	return err
}

func (s *sessionStore) loadSession(ctx context.Context, sessionID string) (*browserSession, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx, `SELECT payload FROM browser_sessions
		WHERE id_digest=$1 AND idle_expires_at>now() AND absolute_expires_at>now()`, digest(sessionID)).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = s.pool.Exec(ctx, `DELETE FROM browser_sessions WHERE id_digest=$1`, digest(sessionID))
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var session browserSession
	if err := s.keys.decrypt("browser-session", payload, &session); err != nil {
		_ = s.deleteSession(ctx, sessionID)
		return nil, err
	}
	return &session, nil
}

func (s *sessionStore) touchSession(ctx context.Context, session browserSession) (bool, error) {
	idleExpiry := s.now().Add(s.idleTTL)
	if session.AbsoluteExpiry.Before(idleExpiry) {
		idleExpiry = session.AbsoluteExpiry
	}
	tag, err := s.pool.Exec(ctx, `UPDATE browser_sessions
		SET last_seen_at=now(), idle_expires_at=$2
		WHERE id_digest=$1 AND idle_expires_at>now() AND absolute_expires_at>now()
		  AND last_seen_at < now()-($3 * interval '1 second')`,
		digest(session.SessionID), idleExpiry, int64(sessionTouchPeriod/time.Second))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}
	var exists bool
	err = s.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM browser_sessions
		WHERE id_digest=$1 AND idle_expires_at>now() AND absolute_expires_at>now()
	)`, digest(session.SessionID)).Scan(&exists)
	return exists, err
}

func (s *sessionStore) deleteSession(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM browser_sessions WHERE id_digest=$1`, digest(sessionID))
	return err
}

func (s *sessionStore) acquireRefreshLease(ctx context.Context, sessionID, owner string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE browser_sessions
		SET refresh_lock_owner=$2, refresh_lock_until=now()+($3 * interval '1 second')
		WHERE id_digest=$1 AND idle_expires_at>now() AND absolute_expires_at>now()
		  AND (refresh_lock_until IS NULL OR refresh_lock_until<=now())`,
		digest(sessionID), owner, int64(refreshLease/time.Second))
	return tag.RowsAffected() == 1, err
}

func (s *sessionStore) replaceAfterRefresh(ctx context.Context, session browserSession, owner string) (bool, error) {
	payload, err := s.keys.encrypt("browser-session", session)
	if err != nil {
		return false, err
	}
	idleExpiry := s.now().Add(s.idleTTL)
	if session.AbsoluteExpiry.Before(idleExpiry) {
		idleExpiry = session.AbsoluteExpiry
	}
	tag, err := s.pool.Exec(ctx, `UPDATE browser_sessions
		SET payload=$2, idle_expires_at=$3, last_seen_at=now(),
		    refresh_lock_owner=NULL, refresh_lock_until=NULL
		WHERE id_digest=$1 AND refresh_lock_owner=$4
		  AND idle_expires_at>now() AND absolute_expires_at>now()`,
		digest(session.SessionID), payload, idleExpiry, owner)
	return tag.RowsAffected() == 1, err
}

func (s *sessionStore) releaseRefreshLease(ctx context.Context, sessionID, owner string) error {
	_, err := s.pool.Exec(ctx, `UPDATE browser_sessions
		SET refresh_lock_owner=NULL, refresh_lock_until=NULL
		WHERE id_digest=$1 AND refresh_lock_owner=$2`, digest(sessionID), owner)
	return err
}

func (s *sessionStore) saveLogout(ctx context.Context, transaction logoutTransaction) (string, error) {
	ticket, err := randomIdentifier()
	if err != nil {
		return "", err
	}
	payload, err := s.keys.encrypt("browser-logout", transaction)
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO browser_logout_transactions(id_digest,payload,expires_at)
		VALUES($1,$2,$3)`, digest(ticket), payload, s.now().Add(s.logoutTTL))
	return ticket, err
}

func (s *sessionStore) takeLogout(ctx context.Context, ticket string) (*logoutTransaction, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx, `DELETE FROM browser_logout_transactions
		WHERE id_digest=$1 AND expires_at>now() RETURNING payload`, digest(ticket)).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var transaction logoutTransaction
	if err := s.keys.decrypt("browser-logout", payload, &transaction); err != nil {
		return nil, err
	}
	return &transaction, nil
}
