package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	memoryKiB uint32 = 19 * 1024
	iterations uint32 = 2
	parallelism uint8 = 1
	saltBytes = 16
	keyBytes = 32
)

var ErrInvalidPassword = errors.New("invalid password")

func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 128 {
		return "", ErrInvalidPassword
	}
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, iterations, memoryKiB, parallelism, keyBytes)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memoryKiB, iterations, parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash)), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var m, t uint32
	var p uint8
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); n != 3 || err != nil {
		return false
	}
	// Hard limits avoid attacker-controlled parameters exhausting server resources.
	if m < memoryKiB || m > 256*1024 || t < iterations || t > 10 || p < 1 || p > 4 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != saltBytes {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != keyBytes {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(expected)))
	return subtle.ConstantTimeCompare(expected, actual) == 1
}

// Strengthening password KDF parameters must preserve the encoded PHC format.
var _ = strconv.IntSize
