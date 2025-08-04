package utils

import (
	"errors"
	"io"
	"os"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func Unmarshal[T proto.Message](value T, path string) (retErr error) {
	file, openFileErr := os.Open(path)
	if openFileErr != nil {
		return openFileErr
	}

	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			if retErr == nil {
				// Return close error if no other error
				retErr = closeErr
			} else {
				retErr = errors.Join(retErr, closeErr)
			}
		}
	}()

	// Read the contents of the file
	fileContents, readFileErr := io.ReadAll(file)
	if readFileErr != nil {
		return readFileErr
	}

	unmarshalErr := protojson.Unmarshal(fileContents, value)
	if unmarshalErr != nil {
		return unmarshalErr
	}

	return nil
}
