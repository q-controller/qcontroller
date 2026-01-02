package utils

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
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

func WaitForFileCreation(filename string, timeout time.Duration) error {
	// First check if file already exists
	if _, err := os.Stat(filename); err == nil {
		return nil // File already exists
	}

	dir := filepath.Dir(filename)
	base := filepath.Base(filename)

	watcher, watcherErr := fsnotify.NewWatcher()
	if watcherErr != nil {
		return watcherErr
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			slog.Error("WaitForFileCreation: failed to close watcher", "error", err)
		}
	}()

	if addErr := watcher.Add(dir); addErr != nil {
		return addErr
	}

	// Check again after starting the watcher to avoid race condition
	if _, statErr := os.Stat(filename); statErr == nil {
		return nil
	}

	slog.Debug("WaitForFileCreation: starting to watch directory", "directory", dir, "filename", base, "timeout", timeout)
	deadline := time.After(timeout)
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("watcher closed")
			}
			slog.Debug("WaitForFileCreation: received event", "event", event.Op, "name", event.Name)
			if event.Op&fsnotify.Create == fsnotify.Create {
				if filepath.Base(event.Name) == base {
					slog.Debug("WaitForFileCreation: target file created", "name", event.Name)
					return nil
				} else {
					slog.Debug("WaitForFileCreation: different file created", "created", filepath.Base(event.Name), "looking_for", base)
				}
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("watcher error channel closed")
			}
		case <-deadline:
			return fmt.Errorf("timeout waiting for %s", filename)
		}
	}
}
