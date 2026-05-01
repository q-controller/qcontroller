package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadOrCreateSecret_GeneratesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.key")

	secret, err := LoadOrCreateSecret(path)
	require.NoError(t, err)
	assert.Len(t, secret, 32)

	// File must exist and contain exactly the returned bytes.
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, secret, written)

	// Mode 0600 (Unix only — on Windows the bits aren't meaningful).
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	}
}

func TestLoadOrCreateSecret_ReturnsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.key")

	first, err := LoadOrCreateSecret(path)
	require.NoError(t, err)

	second, err := LoadOrCreateSecret(path)
	require.NoError(t, err)
	assert.Equal(t, first, second, "subsequent loads must return the same secret")
}

func TestLoadOrCreateSecret_RejectsShortFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.key")

	require.NoError(t, os.WriteFile(path, []byte("too short"), 0o600))

	_, err := LoadOrCreateSecret(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shorter than 32 bytes")
}

func TestLoadOrCreateSecret_PropagatesUnexpectedErrors(t *testing.T) {
	// Pointing at a path inside a non-existent directory must fail with the
	// underlying write error, not silently succeed.
	dir := t.TempDir()
	path := filepath.Join(dir, "missing-subdir", "session.key")

	_, err := LoadOrCreateSecret(path)
	require.Error(t, err)
}
