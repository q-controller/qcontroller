package utils

import (
	_ "embed"
	"fmt"
	"log"
	"log/slog"

	"github.com/q-controller/qcontroller/src/pkg/images"
	"gopkg.in/yaml.v3"
)

const (
	Tag        = "ImageService"
	PathPrefix = "/v1/images"
)

//go:embed docs/openapi.yaml
var openAPISpecs string

func GenerateOpenAPISpecs() (string, error) {
	var spec map[string]interface{}
	if err := yaml.Unmarshal([]byte(openAPISpecs), &spec); err != nil {
		log.Fatalf("Failed to parse OpenAPI spec: %v", err)
	}

	if existingTags, ok := spec["tags"].([]string); ok {
		// Avoid duplicates
		found := false
		for _, t := range existingTags {
			if t == Tag {
				found = true
				break
			}
		}
		if !found {
			spec["tags"] = append(existingTags, Tag)
		}
	} else {
		// Create new tags array
		spec["tags"] = []string{Tag}
	}

	var imagesSpec map[string]interface{}
	if unmarshalErr := yaml.Unmarshal([]byte(images.GetOpenAPISpec(PathPrefix, Tag)), &imagesSpec); unmarshalErr == nil {
		if paths, ok := spec["paths"].(map[string]interface{}); ok {
			for k, v := range imagesSpec {
				paths[k] = v
			}
		}
	} else {
		slog.Warn("Failed to unmarshal images OpenAPI spec", "error", unmarshalErr)
	}

	bytes, bytesErr := yaml.Marshal(spec)
	if bytesErr != nil {
		return "", fmt.Errorf("failed to marshal OpenAPI spec: %w", bytesErr)
	}
	return string(bytes), nil
}
