package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")

	type payload struct {
		Sub  string `json:"sub"`
		Tags []int  `json:"tags"`
	}
	in := payload{Sub: "alice@example.com", Tags: []int{1, 2, 3}}

	encoded, err := encodeSigned(secret, in, time.Hour)
	require.NoError(t, err)

	var out payload
	require.NoError(t, decodeSigned(secret, encoded, &out))
	assert.Equal(t, in, out)
}

func TestDecodeRejectsTamperedPayload(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	encoded, err := encodeSigned(secret, map[string]string{"u": "alice"}, time.Hour)
	require.NoError(t, err)

	dot := strings.IndexByte(encoded, '.')
	require.Greater(t, dot, 0)
	// Flip a bit in the payload portion (before the signature).
	tampered := []byte(encoded)
	tampered[0] ^= 0x01
	encodedTampered := string(tampered)
	if encodedTampered == encoded {
		// First byte happened to be unflippable; pick the second.
		tampered[1] ^= 0x01
		encodedTampered = string(tampered)
	}

	var out map[string]string
	err = decodeSigned(secret, encodedTampered, &out)
	require.Error(t, err)
}

func TestDecodeRejectsTamperedSignature(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	encoded, err := encodeSigned(secret, map[string]string{"u": "alice"}, time.Hour)
	require.NoError(t, err)

	// Last byte of the signature.
	tampered := []byte(encoded)
	tampered[len(tampered)-1] ^= 0x01

	var out map[string]string
	err = decodeSigned(secret, string(tampered), &out)
	require.Error(t, err)
}

func TestDecodeRejectsWrongSecret(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	other := []byte("ffffffffffffffffffffffffffffffff")

	encoded, err := encodeSigned(secret, map[string]string{"u": "alice"}, time.Hour)
	require.NoError(t, err)

	var out map[string]string
	err = decodeSigned(other, encoded, &out)
	require.Error(t, err)
}

func TestDecodeRejectsExpired(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")

	// TTL of -1s ensures the payload is already expired at encode time.
	encoded, err := encodeSigned(secret, map[string]string{"u": "alice"}, -time.Second)
	require.NoError(t, err)

	var out map[string]string
	err = decodeSigned(secret, encoded, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestDecodeRejectsMalformed(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")

	cases := []string{
		"",
		"no-dot-here",
		".",
		"foo.bar",                  // valid shape but invalid base64 + invalid sig
		strings.Repeat("a.b", 100), // multiple dots, garbage content
	}
	for _, c := range cases {
		var out map[string]string
		assert.Error(t, decodeSigned(secret, c, &out), "case %q should fail", c)
	}
}
