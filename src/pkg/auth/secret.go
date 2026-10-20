package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
)

const secretSize = 32

// LoadOrCreateSecret reads a 32-byte secret key from path. If the file does
// not exist, it generates one and writes it with mode 0600.
func LoadOrCreateSecret(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) < secretSize {
			return nil, fmt.Errorf("session secret %s is shorter than %d bytes", path, secretSize)
		}
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read session secret %s: %w", path, err)
	}

	secret := make([]byte, secretSize)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate session secret: %w", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		// Another process won the race; read what it wrote.
		return LoadOrCreateSecret(path)
	}
	if err != nil {
		return nil, fmt.Errorf("create session secret %s: %w", path, err)
	}

	_, writeErr := f.Write(secret)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return nil, fmt.Errorf("write session secret %s: %w", path, err)
	}

	return secret, nil
}
