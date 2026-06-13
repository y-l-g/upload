package upload

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseIntentValidatesAndNormalizes(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	intent, apiErr := parseIntent(`{
		"key":"users/123/avatar.jpg",
		"filename":"avatar.jpg",
		"content_types":["Image/JPEG; charset=binary"],
		"max_bytes":512,
		"overwrite":true,
		"expires_in":60,
		"metadata":{"user_id":"123"}
	}`, defaultStoreName, 1024, time.Minute, now)
	if apiErr != nil {
		t.Fatalf("parse intent failed: %v", apiErr)
	}

	if intent.Store != defaultStoreName || intent.Key != "users/123/avatar.jpg" {
		t.Fatalf("unexpected intent: %#v", intent)
	}
	if len(intent.ContentTypes) != 1 || intent.ContentTypes[0] != "image/jpeg" {
		t.Fatalf("content type was not normalized: %#v", intent.ContentTypes)
	}
	if intent.ExpiresAt.Sub(now) != time.Minute {
		t.Fatalf("unexpected expiry: %s", intent.ExpiresAt)
	}
	if intent.UploadID == "" {
		t.Fatal("upload id was not generated")
	}
}

func TestParseIntentRejectsUnsafeKeys(t *testing.T) {
	tests := []string{
		"../avatar.jpg",
		"/avatar.jpg",
		"users//avatar.jpg",
		"users/./avatar.jpg",
		"users/../avatar.jpg",
		`users\avatar.jpg`,
	}

	for _, key := range tests {
		raw, _ := json.Marshal(map[string]any{
			"key":       key,
			"max_bytes": 1,
		})
		if _, apiErr := parseIntent(string(raw), defaultStoreName, 1024, time.Minute, time.Now()); apiErr == nil || apiErr.kind != errKindValue {
			t.Fatalf("expected value error for key %q, got %v", key, apiErr)
		}
	}
}

func TestParseIntentRejectsMetadataNonStringValues(t *testing.T) {
	_, apiErr := parseIntent(`{"key":"avatar.jpg","max_bytes":1,"metadata":{"user_id":123}}`, defaultStoreName, 1024, time.Minute, time.Now())
	if apiErr == nil || apiErr.kind != errKindValue {
		t.Fatalf("expected value error, got %v", apiErr)
	}
}

func TestParseIntentRejectsMaxBytesAboveStoreLimit(t *testing.T) {
	_, apiErr := parseIntent(`{"key":"avatar.jpg","max_bytes":2048}`, defaultStoreName, 1024, time.Minute, time.Now())
	if apiErr == nil || apiErr.kind != errKindValue {
		t.Fatalf("expected value error, got %v", apiErr)
	}
}
