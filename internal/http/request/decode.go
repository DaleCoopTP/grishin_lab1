package request

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
)

const maxBodyBytes = 1 << 20

// DecodeJSON strictly decodes a single JSON object into dst and returns its raw
// top-level fields so callers can check nested required fields.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, required ...string) (map[string]json.RawMessage, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, errors.New("Content-Type must be application/json")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	fields, err := requiredFields(body, required...)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return nil, fmt.Errorf("decode request body: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("request body must contain one JSON object")
	}
	return fields, nil
}

// RequireFields checks that a nested JSON object contains non-null fields.
func RequireFields(fields map[string]json.RawMessage, object string, names ...string) error {
	if _, err := requiredFields(fields[object], names...); err != nil {
		return fmt.Errorf("%s: %w", object, err)
	}
	return nil
}

func requiredFields(body []byte, names ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, errors.New("expected a JSON object")
	}
	for _, name := range names {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("%s is required", name)
		}
	}
	return fields, nil
}
