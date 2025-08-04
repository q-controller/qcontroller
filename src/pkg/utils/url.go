package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
)

func IsHTTP(rawURL string) bool {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return parsedURL.Scheme == "http" || parsedURL.Scheme == "https"
}

func Hash(s string) string {
	hash := sha256.New()
	hash.Write([]byte(s))
	return hex.EncodeToString(hash.Sum(nil))
}
