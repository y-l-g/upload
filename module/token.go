package upload

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	errTokenMalformed = errors.New("malformed upload token")
	errTokenInvalid   = errors.New("invalid upload token")
	errTokenExpired   = errors.New("expired upload token")
)

type tokenHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

type tokenClaims struct {
	Version      int               `json:"v"`
	UploadID     string            `json:"uid"`
	Store        string            `json:"store"`
	Key          string            `json:"key"`
	Filename     string            `json:"filename,omitempty"`
	ContentTypes []string          `json:"ct,omitempty"`
	MaxBytes     int64             `json:"max"`
	Overwrite    bool              `json:"ow,omitempty"`
	Metadata     map[string]string `json:"meta,omitempty"`
	IssuedAt     int64             `json:"iat"`
	ExpiresAt    int64             `json:"exp"`
}

func signIntent(intent uploadIntent, secret string, now time.Time) (string, error) {
	claims := tokenClaims{
		Version:      1,
		UploadID:     intent.UploadID,
		Store:        intent.Store,
		Key:          intent.Key,
		Filename:     intent.Filename,
		ContentTypes: intent.ContentTypes,
		MaxBytes:     intent.MaxBytes,
		Overwrite:    intent.Overwrite,
		Metadata:     intent.Metadata,
		IssuedAt:     now.UTC().Unix(),
		ExpiresAt:    intent.ExpiresAt.UTC().Unix(),
	}

	headerJSON, err := json.Marshal(tokenHeader{Algorithm: "HS256", Type: "JWT"})
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	payload := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := header + "." + payload
	signature := signBytes([]byte(signingInput), []byte(secret))

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func verifyToken(token string, secret string, now time.Time) (uploadIntent, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return uploadIntent{}, errTokenMalformed
	}

	signingInput := parts[0] + "." + parts[1]
	expected := signBytes([]byte(signingInput), []byte(secret))
	actual, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return uploadIntent{}, errTokenMalformed
	}
	if !hmac.Equal(actual, expected) {
		return uploadIntent{}, errTokenInvalid
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return uploadIntent{}, errTokenMalformed
	}
	var header tokenHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return uploadIntent{}, errTokenMalformed
	}
	if header.Algorithm != "HS256" || header.Type != "JWT" {
		return uploadIntent{}, errTokenInvalid
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return uploadIntent{}, errTokenMalformed
	}
	var claims tokenClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return uploadIntent{}, errTokenMalformed
	}
	if err := validateClaims(claims); err != nil {
		return uploadIntent{}, err
	}
	if now.UTC().Unix() > claims.ExpiresAt {
		return uploadIntent{}, errTokenExpired
	}

	return uploadIntent{
		UploadID:     claims.UploadID,
		Store:        claims.Store,
		Key:          claims.Key,
		Filename:     claims.Filename,
		ContentTypes: claims.ContentTypes,
		MaxBytes:     claims.MaxBytes,
		Overwrite:    claims.Overwrite,
		Metadata:     claims.Metadata,
		ExpiresAt:    time.Unix(claims.ExpiresAt, 0).UTC(),
	}, nil
}

func validateClaims(claims tokenClaims) error {
	if claims.Version != 1 {
		return errTokenInvalid
	}
	if claims.UploadID == "" || !strings.HasPrefix(claims.UploadID, "upl_") {
		return errTokenInvalid
	}
	if claims.Store == "" || claims.Key == "" || claims.MaxBytes <= 0 || claims.ExpiresAt <= 0 {
		return errTokenInvalid
	}
	if err := validateObjectKey(claims.Key); err != nil {
		return errTokenInvalid
	}
	for _, contentType := range claims.ContentTypes {
		if _, err := normalizeContentType(contentType); err != nil {
			return errTokenInvalid
		}
	}
	for key, value := range claims.Metadata {
		if key == "" || strings.ContainsRune(key, '\x00') || strings.ContainsRune(value, '\x00') {
			return errTokenInvalid
		}
	}
	return nil
}

func signBytes(message []byte, secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}

func tokenErrorMessage(err error) string {
	switch {
	case errors.Is(err, errTokenExpired):
		return "upload token expired"
	case errors.Is(err, errTokenMalformed):
		return "upload token is malformed"
	case errors.Is(err, errTokenInvalid):
		return "upload token is invalid"
	default:
		return fmt.Sprintf("upload token rejected: %v", err)
	}
}
