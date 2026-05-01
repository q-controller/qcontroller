package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type signedEnvelope struct {
	Data json.RawMessage `json:"data"`
	Exp  int64           `json:"exp"`
}

// encodeSigned produces "<base64url(json(envelope))>.<base64url(hmac-sha256)>".
// The envelope contains the marshaled value and an absolute expiry.
func encodeSigned(secret []byte, v any, ttl time.Duration) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	env := signedEnvelope{Data: data, Exp: time.Now().Add(ttl).Unix()}
	envBytes, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString(envBytes)

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(enc))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return enc + "." + sig, nil
}

// decodeSigned verifies the HMAC and expiry, then unmarshals into v.
func decodeSigned(secret []byte, raw string, v any) error {
	dot := strings.IndexByte(raw, '.')
	if dot < 0 {
		return errors.New("malformed signed value")
	}
	enc, sig := raw[:dot], raw[dot+1:]

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(enc))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return errors.New("bad signature")
	}

	envBytes, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return err
	}
	var env signedEnvelope
	if err := json.Unmarshal(envBytes, &env); err != nil {
		return err
	}
	if time.Now().Unix() > env.Exp {
		return errors.New("expired")
	}
	return json.Unmarshal(env.Data, v)
}
