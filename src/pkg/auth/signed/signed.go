package signed

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"
)

var (
	ErrMalformedToken = errors.New("malformed signed token")
	ErrBadSignature   = errors.New("bad signature")
	ErrTruncatedToken = errors.New("truncated signed token")
	ErrExpiredToken   = errors.New("expired")
)

// Token wire form: enc + "." + sig
//
//	enc = base64url(exp || payload), where exp = unix seconds, 8 bytes big-endian
//	sig = base64url(hmac_sha256(key, enc))
type Token struct {
	enc string
	sig string
}

func FromString(raw string) (Token, error) {
	enc, sig, ok := strings.Cut(raw, ".")
	if !ok || enc == "" || sig == "" {
		return Token{}, ErrMalformedToken
	}
	return Token{enc: enc, sig: sig}, nil
}

func (t Token) String() string {
	return t.enc + "." + t.sig
}

func Sign(key, payload []byte, ttl time.Duration) Token {
	buf := binary.BigEndian.AppendUint64(make([]byte, 0, 8+len(payload)), uint64(time.Now().Add(ttl).Unix()))
	enc := base64.RawURLEncoding.EncodeToString(append(buf, payload...))
	return Token{enc: enc, sig: base64.RawURLEncoding.EncodeToString(mac(key, enc))}
}

func Verify(key []byte, t Token) ([]byte, error) {
	sig, err := base64.RawURLEncoding.DecodeString(t.sig)
	if err != nil || !hmac.Equal(sig, mac(key, t.enc)) {
		return nil, ErrBadSignature
	}
	buf, err := base64.RawURLEncoding.DecodeString(t.enc)
	if err != nil {
		return nil, err
	}
	if len(buf) < 8 {
		return nil, ErrTruncatedToken
	}
	if time.Now().Unix() > int64(binary.BigEndian.Uint64(buf)) {
		return nil, ErrExpiredToken
	}
	return buf[8:], nil
}

func mac(key []byte, enc string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(enc))
	return m.Sum(nil)
}
