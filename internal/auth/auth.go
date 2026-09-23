package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

func GenerateToken(prefix string) string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(b)
}

func HashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return "sha256$" + base64.RawStdEncoding.EncodeToString(hash[:])
}

func VerifyToken(token, encoded string) bool {
	if strings.HasPrefix(encoded, "sha256$") {
		expected, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encoded, "sha256$"))
		if err != nil || len(expected) != sha256.Size {
			return false
		}
		actual := sha256.Sum256([]byte(token))
		return subtle.ConstantTimeCompare(actual[:], expected) == 1
	}
	if strings.HasPrefix(encoded, "argon2id$") {
		return verifyArgon2ID(token, encoded)
	}
	return false
}

func verifyArgon2ID(token, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" {
		return false
	}
	version, err := strconv.Atoi(strings.TrimPrefix(parts[1], "v="))
	if err != nil || version != argon2.Version {
		return false
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	if memory == 0 || iterations == 0 || parallelism == 0 || memory > 1024*1024 || iterations > 20 || parallelism > 32 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(expected) == 0 || len(expected) > 64 {
		return false
	}
	hashLength := len(expected)
	if uint64(hashLength) > uint64(^uint32(0)) {
		return false
	}
	actual := argon2.IDKey([]byte(token), salt, iterations, memory, parallelism, uint32(hashLength))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
