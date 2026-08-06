package webhookcore

import (
	"bytes"
	"encoding/json"
	"fmt"

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
