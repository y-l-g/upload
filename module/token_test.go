package upload

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSignAndVerifyTokenRoundTrip(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	intent := uploadIntent{
		UploadID:     "upl_0123456789abcdef0123456789abcdef",
		Store:        defaultStoreName,
		Key:          "users/123/avatar.jpg",
		Filename:     "avatar.jpg",
		ContentTypes: []string{"image/jpeg"},
		MaxBytes:     512,
		Metadata:     map[string]string{"user_id": "123"},
		ExpiresAt:    now.Add(time.Minute),
	}

	token, err := signIntent(intent, "secret", now)
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	verified, err := verifyToken(token, "secret", now)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}

	if verified.UploadID != intent.UploadID || verified.Key != intent.Key || verified.MaxBytes != intent.MaxBytes {
		t.Fatalf("unexpected verified intent: %#v", verified)
	}
}

func TestVerifyTokenRejectsTampering(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	intent := uploadIntent{
		UploadID:  "upl_0123456789abcdef0123456789abcdef",
		Store:     defaultStoreName,
		Key:       "avatar.jpg",
		MaxBytes:  1,
		ExpiresAt: now.Add(time.Minute),
	}
	token, err := signIntent(intent, "secret", now)
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	replacement := "x"
	if strings.HasSuffix(token, replacement) {
		replacement = "y"
	}
	tampered := token[:len(token)-1] + replacement
	if _, err := verifyToken(tampered, "secret", now); !errors.Is(err, errTokenInvalid) {
		t.Fatalf("expected invalid token, got %v", err)
	}
}

func TestVerifyTokenRejectsExpiredTokens(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	intent := uploadIntent{
		UploadID:  "upl_0123456789abcdef0123456789abcdef",
		Store:     defaultStoreName,
		Key:       "avatar.jpg",
		MaxBytes:  1,
		ExpiresAt: now.Add(time.Second),
	}
	token, err := signIntent(intent, "secret", now)
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}

	if _, err := verifyToken(token, "secret", now.Add(2*time.Second)); !errors.Is(err, errTokenExpired) {
		t.Fatalf("expected expired token, got %v", err)
	}
}
