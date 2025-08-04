package utils

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"
)

var downloadGroup singleflight.Group

func downloadFile(url, filepath string) (retErr error) {
	slog.Info("Starting file download", "url", url)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to send HTTP request: %v", err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			if retErr == nil {
				// Return close error if no other error
				retErr = closeErr
			} else {
				retErr = errors.Join(retErr, closeErr)
			}
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d %s", resp.StatusCode, resp.Status)
	}

	size, err := strconv.Atoi(resp.Header.Get("Content-Length"))
	if err != nil {
		size = -1 // Unknown size
	}

	out, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %v", filepath, err)
	}

	defer func() {
		if closeErr := out.Close(); closeErr != nil {
			if retErr == nil {
				// Return close error if no other error
				retErr = closeErr
			} else {
				retErr = errors.Join(retErr, closeErr)
			}
		}
	}()

	progress := &progressWriter{total: size}
	_, err = io.Copy(io.MultiWriter(out, progress), resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file %s: %v", filepath, err)
	}

	slog.Info("File downloaded successfully", "filepath", filepath)
	return nil
}

func DownloadFile(url, filepath string) (downloadFileErr error) {
	_, err, _ := downloadGroup.Do(url, func() (interface{}, error) {
		tempFile := fmt.Sprintf("DownloadFile-%d", time.Now().UnixMilli())
		file, fileErr := os.CreateTemp(os.TempDir(), tempFile)

		if fileErr != nil {
			return "", fileErr
		}

		defer func() {
			if closeErr := file.Close(); closeErr != nil {
				if downloadFileErr == nil {
					// Return close error if no other error
					downloadFileErr = closeErr
				} else {
					downloadFileErr = errors.Join(downloadFileErr, closeErr)
				}
			}

			if rmErr := os.Remove(file.Name()); rmErr != nil {
				if downloadFileErr == nil {
					// Return close error if no other error
					downloadFileErr = rmErr
				} else {
					downloadFileErr = errors.Join(downloadFileErr, rmErr)
				}
			}
		}()

		if downloadErr := downloadFile(url, file.Name()); downloadErr != nil {
			return nil, downloadErr
		}
		return nil, CopyFile(file.Name(), filepath)
	})
	return err
}

type progressWriter struct {
	total   int
	written int
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.written += n
	if pw.total > 0 {
		slog.Debug("Download progress", "progress", fmt.Sprintf("%.2f%%", float64(pw.written)/float64(pw.total)*100))
	} else {
		slog.Debug("Download progress", "bytes", pw.written)
	}
	return n, nil
}
