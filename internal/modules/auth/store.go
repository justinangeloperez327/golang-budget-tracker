package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	DB *pgxpool.Pool
	SessionTTL time.Duration
}

type Session struct {
	Actor Actor
	Token string
	ExpiresAt time.Time
}

func NewStore(db *pgxpool.Pool, ttl time.Duration) *Store {
	return &Store{DB: db, SessionTTL: ttl}
}

// allow is a persistent fixed-window limiter. IP identifiers never use untrusted forwarding headers.
func (s *Store) allow(ctx context.Context, category, identity string, max int) (bool, error) {
	key := sha256.Sum256([]byte(category + ":" + identity))
	var count int
	err := s.DB.QueryRow(ctx, `
		INSERT INTO auth_attempts (key_hash, count, window_started)
		VALUES ($1, 1, now())
		ON CONFLICT (key_hash) DO UPDATE
		SET count = CASE WHEN auth_attempts.window_started < now() - interval '15 minutes'
		    THEN 1 ELSE auth_attempts.count + 1 END,
		    window_started = CASE WHEN auth_attempts.window_started < now() - interval '15 minutes'
		    THEN now() ELSE auth_attempts.window_started END
		RETURNING count
	`, key[:]).Scan(&count)
	if err != nil {
		return false, err
	}
	return count <= max, nil
}

func (s *Store) LimitRegistration(ctx context.Context, ip string) error {
	ok, err := s.allow(ctx, "register-ip", ip, 6)
	if err != nil {
		return err
	}
	if !ok {
		return ErrLimited
	}
	return nil
}

func (s *Store) Register(ctx context.Context, emailRaw, password string) (Session, error) {
	email, err := NormalizeEmail(emailRaw)
	if err != nil {
		return Session{}, err
	}
	passwordHash, err := HashPassword(password)
	if err != nil {
		return Session{}, err
	}
	userID, err := randomHex(16)
	if err != nil { return Session{}, err }
	workspaceID, err := randomHex(16)
	if err != nil { return Session{}, err }
	token, err := randomHex(32)
	if err != nil { return Session{}, err }
	csrf, err := randomHex(32)
	if err != nil { return Session{}, err }
	hash, _ := tokenHash(token)
	expiry := time.Now().UTC().Add(s.SessionTTL)
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO users (id, email, password_hash) VALUES ($1,$2,$3)`,
			userID, email, passwordHash); err != nil { return err }
		if _, err := tx.Exec(ctx,
			`INSERT INTO workspaces (id, owner_user_id, name) VALUES ($1,$2,'Personal')`,
			workspaceID, userID); err != nil { return err }
		_, err := tx.Exec(ctx,
			`INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at) VALUES ($1,$2,$3,$4)`,
			hash[:], userID, csrf, expiry)
		return err
	})
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23505" {
			return Session{}, ErrConflict
		}
		return Session{}, fmt.Errorf("register account: %w", err)
	}
	return Session{
		Actor: Actor{UserID:userID, WorkspaceID:workspaceID, Email:email, CSRFToken:csrf},
		Token: token, ExpiresAt:expiry,
	}, nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

func dummyPasswordHash() string {
	dummyOnce.Do(func() { dummyHash, _ = HashPassword("nonexistent-account-password") })
	return dummyHash
}

func (s *Store) Login(ctx context.Context, emailRaw, password, ip string) (Session, error) {
	email, err := NormalizeEmail(emailRaw)
	if err != nil { return Session{}, ErrCredentials }
	if len(password) < 12 || len(password) > 128 {
		return Session{}, ErrCredentials
	}
	if ok, err := s.allow(ctx, "login-ip", ip, 30); err != nil { return Session{}, err } else if !ok { return Session{}, ErrLimited }
	if ok, err := s.allow(ctx, "login-email", email, 20); err != nil { return Session{}, err } else if !ok { return Session{}, ErrLimited }
	var userID, passwordHash string
	err = s.DB.QueryRow(ctx, `SELECT id, password_hash FROM users WHERE email=$1`, email).Scan(&userID, &passwordHash)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) { return Session{}, err }
		_ = VerifyPassword(dummyPasswordHash(), password)
		return Session{}, ErrCredentials
	}
	if !VerifyPassword(passwordHash, password) {
		return Session{}, ErrCredentials
	}
	token, err := randomHex(32)
	if err != nil { return Session{}, err }
	csrf, err := randomHex(32)
	if err != nil { return Session{}, err }
	hash, _ := tokenHash(token)
	expiry := time.Now().UTC().Add(s.SessionTTL)
	if _, err := s.DB.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at) VALUES ($1,$2,$3,$4)`,
		hash[:], userID, csrf, expiry); err != nil { return Session{}, err }
	actor, err := s.Authenticate(ctx, token)
	if err != nil { return Session{}, err }
	return Session{Actor:actor, Token:token, ExpiresAt:expiry}, nil
}

func (s *Store) Authenticate(ctx context.Context, token string) (Actor, error) {
	hash, ok := tokenHash(token)
	if !ok { return Actor{}, ErrUnauthenticated }
	var actor Actor
	err := s.DB.QueryRow(ctx, `
		SELECT u.id, w.id, u.email, se.csrf_token
		FROM sessions se
		JOIN users u ON u.id = se.user_id
		JOIN workspaces w ON w.owner_user_id = u.id
		WHERE se.token_hash=$1 AND se.expires_at > now()
	`, hash[:]).Scan(&actor.UserID, &actor.WorkspaceID, &actor.Email, &actor.CSRFToken)
	if errors.Is(err, pgx.ErrNoRows) { return Actor{}, ErrUnauthenticated }
	if err != nil { return Actor{}, err }
	return actor, nil
}

func (s *Store) Logout(ctx context.Context, token string) error {
	hash, ok := tokenHash(token)
	if !ok { return ErrUnauthenticated }
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, hash[:])
	return err
}

func (s *Store) WorkspaceName(ctx context.Context, actor Actor, workspaceID string) (string, error) {
	if workspaceID != actor.WorkspaceID {
		return "", ErrUnauthenticated
	}
	var name string
	err := s.DB.QueryRow(ctx,
		`SELECT name FROM workspaces WHERE id=$1 AND owner_user_id=$2`,
		workspaceID, actor.UserID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) { return "", ErrUnauthenticated }
	return name, err
}

func RemoteIP(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil || strings.TrimSpace(host) == "" { return "unknown" }
	return host
}
