package parser

import (
	"crypto/sha256"
	"encoding/hex"
)

// sha256NewSum hashes the joined input parts (each terminated by NUL so
// "ab" + "c" hashes differently from "a" + "bc").
func sha256NewSum(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}