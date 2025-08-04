package utils

import (
	"os"
	"time"
)

func CopyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	return os.WriteFile(dst, data, 0644)
}

func TouchFile(path string) error {
	now := time.Now()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return os.Chtimes(path, now, now)
}
