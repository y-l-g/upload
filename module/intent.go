package upload

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"path"
	"strings"
	"time"
)

type uploadIntent struct {
	UploadID     string            `json:"upload_id"`
	Store        string            `json:"store"`
	Key          string            `json:"key"`
	Filename     string            `json:"filename,omitempty"`
	ContentTypes []string          `json:"content_types,omitempty"`
	MaxBytes     int64             `json:"max_bytes"`
	Overwrite    bool              `json:"overwrite,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	ExpiresAt    time.Time         `json:"expires_at"`
}

type createResponse struct {
	UploadID  string            `json:"upload_id"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt string            `json:"expires_at"`
	MaxBytes  int64             `json:"max_bytes"`
}

func parseIntent(raw string, storeName string, maxUploadBytes int64, defaultTTL time.Duration, now time.Time) (uploadIntent, *apiError) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return uploadIntent{}, valueError("upload intent must be a JSON-compatible array")
	}

	key, apiErr := requiredString(fields, "key")
	if apiErr != nil {
		return uploadIntent{}, apiErr
	}
	if err := validateObjectKey(key); err != nil {
		return uploadIntent{}, valueError("%s", err.Error())
	}

	maxBytes, apiErr := requiredPositiveInt64(fields, "max_bytes")
	if apiErr != nil {
		return uploadIntent{}, apiErr
	}
	if maxBytes > maxUploadBytes {
		return uploadIntent{}, valueError("max_bytes exceeds store max_upload_bytes")
	}

	filename, apiErr := optionalString(fields, "filename")
	if apiErr != nil {
		return uploadIntent{}, apiErr
	}
	contentTypes, apiErr := optionalStringList(fields, "content_types")
	if apiErr != nil {
		return uploadIntent{}, apiErr
	}
	for i, contentType := range contentTypes {
		normalized, err := normalizeContentType(contentType)
		if err != nil {
			return uploadIntent{}, valueError("content_types[%d] is invalid", i)
		}
		contentTypes[i] = normalized
	}

	overwrite, apiErr := optionalBool(fields, "overwrite")
	if apiErr != nil {
		return uploadIntent{}, apiErr
	}

	expiresIn := defaultTTL
	if rawExpires, ok := fields["expires_in"]; ok && len(rawExpires) > 0 && string(rawExpires) != "null" {
		seconds, apiErr := rawPositiveInt64(rawExpires, "expires_in")
		if apiErr != nil {
			return uploadIntent{}, apiErr
		}
		expiresIn = time.Duration(seconds) * time.Second
	}

	metadata, apiErr := optionalStringMap(fields, "metadata")
	if apiErr != nil {
		return uploadIntent{}, apiErr
	}

	uploadID, err := newUploadID()
	if err != nil {
		return uploadIntent{}, runtimeErrorWrap(err, "failed to generate upload id")
	}

	return uploadIntent{
		UploadID:     uploadID,
		Store:        storeName,
		Key:          key,
		Filename:     filename,
		ContentTypes: contentTypes,
		MaxBytes:     maxBytes,
		Overwrite:    overwrite,
		Metadata:     metadata,
		ExpiresAt:    now.Add(expiresIn).UTC(),
	}, nil
}

func requiredString(fields map[string]json.RawMessage, name string) (string, *apiError) {
	raw, ok := fields[name]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return "", valueError("%s is required", name)
	}
	return rawString(raw, name)
}

func optionalString(fields map[string]json.RawMessage, name string) (string, *apiError) {
	raw, ok := fields[name]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	return rawString(raw, name)
}

func rawString(raw json.RawMessage, name string) (string, *apiError) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", valueError("%s must be a string", name)
	}
	if strings.ContainsRune(value, '\x00') {
		return "", valueError("%s must not contain NUL bytes", name)
	}
	return value, nil
}

func requiredPositiveInt64(fields map[string]json.RawMessage, name string) (int64, *apiError) {
	raw, ok := fields[name]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return 0, valueError("%s is required", name)
	}
	return rawPositiveInt64(raw, name)
}

func rawPositiveInt64(raw json.RawMessage, name string) (int64, *apiError) {
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, valueError("%s must be an integer", name)
	}
	if value <= 0 {
		return 0, valueError("%s must be positive", name)
	}
	return value, nil
}

func optionalBool(fields map[string]json.RawMessage, name string) (bool, *apiError) {
	raw, ok := fields[name]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return false, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, valueError("%s must be a boolean", name)
	}
	return value, nil
}

func optionalStringList(fields map[string]json.RawMessage, name string) ([]string, *apiError) {
	raw, ok := fields[name]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, valueError("%s must be a string array", name)
	}
	for i, value := range values {
		if value == "" {
			return nil, valueError("%s[%d] must not be empty", name, i)
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, valueError("%s[%d] must not contain NUL bytes", name, i)
		}
	}
	return values, nil
}

func optionalStringMap(fields map[string]json.RawMessage, name string) (map[string]string, *apiError) {
	raw, ok := fields[name]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var values map[string]string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, valueError("%s must be a string map", name)
	}
	for key, value := range values {
		if key == "" {
			return nil, valueError("%s keys must not be empty", name)
		}
		if strings.ContainsRune(key, '\x00') || strings.ContainsRune(value, '\x00') {
			return nil, valueError("%s must not contain NUL bytes", name)
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	return values, nil
}

func validateObjectKey(key string) error {
	if key == "" {
		return fmt.Errorf("key is required")
	}
	if strings.ContainsRune(key, '\x00') {
		return fmt.Errorf("key must not contain NUL bytes")
	}
	if strings.HasPrefix(key, "/") {
		return fmt.Errorf("key must be relative")
	}
	if key == "." || key == ".." {
		return fmt.Errorf("key must be a normalized relative path")
	}
	if strings.Contains(key, "\\") {
		return fmt.Errorf("key must use forward slashes")
	}

	cleaned := path.Clean(key)
	if cleaned != key {
		return fmt.Errorf("key must be normalized")
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("key must not contain empty, current, or parent path segments")
		}
	}
	return nil
}

func normalizeContentType(value string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return "", err
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "" || !strings.Contains(mediaType, "/") {
		return "", fmt.Errorf("invalid content type")
	}
	return mediaType, nil
}

func newUploadID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "upl_" + hex.EncodeToString(b[:]), nil
}
