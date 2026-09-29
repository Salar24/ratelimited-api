// Package shortcode generates and validates short link codes.
package shortcode

import (
	"crypto/rand"
	"math/big"
	"regexp"
)

const (
	alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	// DefaultLength gives 62^7 ≈ 3.5 trillion codes.
	DefaultLength = 7
)

var validCode = regexp.MustCompile(`^[0-9A-Za-z_-]{3,32}$`)

// Generate returns a cryptographically random base62 code of length n.
func Generate(n int) (string, error) {
	max := big.NewInt(int64(len(alphabet)))
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[idx.Int64()]
	}
	return string(b), nil
}

// Valid reports whether code is acceptable as a custom alias.
func Valid(code string) bool {
	return validCode.MatchString(code)
}
