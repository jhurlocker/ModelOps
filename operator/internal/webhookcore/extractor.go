package webhookcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/client-go/util/jsonpath"
)

// JSONPathExtractor extracts values from JSON bodies using JSONPath
// expressions. Uses the standard Kubernetes jsonpath implementation --
// it operates on parsed JSON trees, not raw string interpolation, so
// there is no injection or traversal risk against untrusted provider
// responses.
type JSONPathExtractor struct{}

// String extracts the value at jsonPath from body as a string. The body
// must be valid JSON. The path must resolve to exactly one leaf value.
// Result types are stringified: numbers are formatted, booleans are
// "true"/"false", null is an empty string.
func (JSONPathExtractor) String(body []byte, jsonPath string) (string, error) {
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("parsing json: %w", err)
	}

	j := jsonpath.New("webhook-extractor")
	if err := j.Parse(jsonPath); err != nil {
		return "", fmt.Errorf("parsing jsonpath %q: %w", jsonPath, err)
	}

	var buf bytes.Buffer
	if err := j.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("extracting jsonpath %q: %w", jsonPath, err)
	}

	result := buf.String()
	if result == "" {
		return "", fmt.Errorf("jsonpath %q: no value found", jsonPath)
	}
	return result, nil
}

// Slice extracts the value at jsonPath from body as a []any slice. The
// body must be valid JSON and the path must resolve to a JSON array.
// Returns nil, nil when the path does not match anything -- callers
// treat this as "no evidence extracted," not an error.
func (JSONPathExtractor) Slice(body []byte, jsonPath string) ([]any, error) {
	if jsonPath == "" {
		return nil, nil
	}
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("parsing json: %w", err)
	}
	v, err := traverse(data, jsonPath)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("jsonpath %q did not resolve to an array", jsonPath)
	}
	return arr, nil
}

func traverse(data any, path string) (any, error) {
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return data, nil
	}
	parts := strings.Split(path, ".")
	cur := data
	for _, part := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, nil
		}
		v, exists := m[part]
		if !exists {
			return nil, nil
		}
		cur = v
	}
	return cur, nil
}
