package signed

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var key = []byte("0123456789abcdef0123456789abcdef")

func TestRoundTrip(t *testing.T) {
	in := []byte(`{"sub":"alice@example.com"}`)

	tok := Sign(key, in, time.Hour)

	parsed, err := FromString(tok.String())
	require.NoError(t, err)
	assert.Equal(t, tok, parsed)

	out, err := Verify(key, parsed)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestRoundTrip_EmptyPayload(t *testing.T) {
	out, err := Verify(key, Sign(key, nil, time.Hour))
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestFromString_RejectsMalformed(t *testing.T) {
	for _, raw := range []string{"", ".", "no-dot-here", "a.", ".b"} {
		_, err := FromString(raw)
		assert.Error(t, err, "case %q should fail", raw)
	}
}

func TestVerify_RejectsTamperedPayload(t *testing.T) {
	raw := []byte(Sign(key, []byte("alice"), time.Hour).String())
	raw[0] ^= 0x01
	tampered, err := FromString(string(raw))
	require.NoError(t, err)

	_, err = Verify(key, tampered)
	require.Error(t, err)
}

func TestVerify_RejectsTamperedSignature(t *testing.T) {
	raw := []byte(Sign(key, []byte("alice"), time.Hour).String())
	raw[len(raw)-1] ^= 0x01
	tampered, err := FromString(string(raw))
	require.NoError(t, err)

	_, err = Verify(key, tampered)
	require.Error(t, err)
}

func TestVerify_RejectsWrongKey(t *testing.T) {
	tok := Sign(key, []byte("alice"), time.Hour)

	_, err := Verify([]byte("ffffffffffffffffffffffffffffffff"), tok)
	require.Error(t, err)
}

func TestVerify_RejectsExpired(t *testing.T) {
	tok := Sign(key, []byte("alice"), -time.Second)

	_, err := Verify(key, tok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestVerify_RejectsGarbageWithValidShape(t *testing.T) {
	for _, raw := range []string{"foo.bar", strings.Repeat("a.b", 100)} {
		tok, err := FromString(raw)
		require.NoError(t, err)
		_, err = Verify(key, tok)
		assert.Error(t, err, "case %q should fail", raw)
	}
}
