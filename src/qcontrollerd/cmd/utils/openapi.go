// TODO: rename to cmd/openapi (this package only holds the OpenAPI generator).
//
//nolint:revive // package name "utils" — see TODO above
package utils

import (
	_ "embed"
	"fmt"
	"log/slog"
	"maps"

	"gopkg.in/yaml.v3"
)

const (
	Tag        = "ImageService"
	PathPrefix = "/v1/images"
)

//go:embed docs/openapi.yaml
var openAPISpecs string

//go:embed docs/image-service-openapi.yml
var imageServiceOpenAPISpecs string

func mergeYAML(base, overlay map[string]any) map[string]any {
	result := make(map[string]any)
	// Copy base
	maps.Copy(result, base)
	// Overlay on top
	for k, v := range overlay {
		if baseVal, ok := base[k]; ok {
			if baseMap, ok1 := baseVal.(map[string]any); ok1 {
				if overlayMap, ok2 := v.(map[string]any); ok2 {
					result[k] = mergeYAML(baseMap, overlayMap) // Recursive
					continue
				}
			}
		}
		result[k] = v // Override or add
	}
	return result
}

func GenerateOpenAPISpecs() (string, error) {
	var spec map[string]any
	if err := yaml.Unmarshal([]byte(openAPISpecs), &spec); err != nil {
		return "", fmt.Errorf("failed to unmarshal base OpenAPI spec: %w", err)
	}

	var imagesSpec map[string]any
	if unmarshalErr := yaml.Unmarshal([]byte(imageServiceOpenAPISpecs), &imagesSpec); unmarshalErr != nil {
		return "", fmt.Errorf("failed to unmarshal images OpenAPI spec: %w", unmarshalErr)
	}

	mergedSpec := mergeYAML(spec, imagesSpec)

	slog.Debug("Generated OpenAPI spec", "spec", mergedSpec)

	bytes, bytesErr := yaml.Marshal(mergedSpec)
	if bytesErr != nil {
		return "", fmt.Errorf("failed to marshal OpenAPI spec: %w", bytesErr)
	}
	return string(bytes), nil
}
