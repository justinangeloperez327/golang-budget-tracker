package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

var (
	ErrInvalidInput    = errors.New("invalid input")
	ErrCredentials     = errors.New("invalid credentials")
	ErrConflict        = errors.New("account already exists")
	ErrLimited         = errors.New("too many attempts")
	ErrUnauthenticated = errors.New("authentication required")
)

type Actor struct {
	UserID      string `json:"user_id"`
	WorkspaceID string `json:"workspace_id"`
	Email       string `json:"email"`
	CSRFToken   string `json:"-"`
}

func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if len(email) < 3 || len(email) > 254 || strings.ContainsAny(email, " \t\r\n") ||
		strings.Count(email, "@") != 1 || strings.HasPrefix(email, "@") || strings.HasSuffix(email, "@") {
		return "", ErrInvalidInput
	}
	return email, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func tokenHash(token string) ([32]byte, bool) {
	var empty [32]byte
	if len(token) != 64 {
		return empty, false
	}
	if _, err := hex.DecodeString(token); err != nil {
		return empty, false
	}
	return sha256.Sum256([]byte(token)), true
}
