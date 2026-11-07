// Package token выпускает API-токены. В базе хранится только sha256 от токена.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

const prefix = "epi_"

// New возвращает токен для клиента и хеш для базы.
func New() (plain string, hash []byte) {
	b := make([]byte, 32)
	rand.Read(b) // crypto/rand.Read не возвращает ошибок с Go 1.24
	plain = prefix + base64.RawURLEncoding.EncodeToString(b)
	return plain, Hash(plain)
}

// Hash — то, что лежит в api_tokens.token_hash.
func Hash(plain string) []byte {
	h := sha256.Sum256([]byte(plain))
	return h[:]
}
