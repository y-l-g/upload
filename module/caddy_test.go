package upload

import (
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func parseUploadConfig(t *testing.T, input string) (*Upload, error) {
	t.Helper()
	d := caddyfile.NewTestDispenser(input)
	u := &Upload{}
	err := u.UnmarshalCaddyfile(d)
	return u, err
}

func TestUnmarshalStoreConfig(t *testing.T) {
	u, err := parseUploadConfig(t, `pogo_upload {
		store default {
			worker public/upload-worker.php
			signing_secret secret
			backend local {
				root storage/app/pogo-uploads
			}
			token_ttl 5m
			max_upload_bytes 4096
			max_concurrency 4
			read_timeout 2s
			complete_timeout 3s
			progress_ttl 4m
		}
	}`)
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(u.Stores) != 1 {
		t.Fatalf("expected one store, got %d", len(u.Stores))
	}

	cfg := applyStoreDefaults(u.Stores[0])
	if cfg.Name != defaultStoreName || cfg.Worker != "public/upload-worker.php" || cfg.Backend.Root != "storage/app/pogo-uploads" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if time.Duration(cfg.TokenTTL) != 5*time.Minute || cfg.MaxUploadBytes != 4096 || cfg.MaxConcurrency != 4 {
		t.Fatalf("unexpected limits: %#v", cfg)
	}
}

func TestUnmarshalStoreConfigRejectsExtraDirectiveArguments(t *testing.T) {
	tests := map[string]string{
		"worker":           "worker public/upload-worker.php extra",
		"signing_secret":   "signing_secret secret extra",
		"root":             "backend local {\n\t\t\t\troot storage/app/pogo-uploads extra\n\t\t\t}",
		"token_ttl":        "token_ttl 5m extra",
		"max_upload_bytes": "max_upload_bytes 4096 extra",
		"max_concurrency":  "max_concurrency 4 extra",
		"read_timeout":     "read_timeout 2s extra",
		"complete_timeout": "complete_timeout 3s extra",
		"progress_ttl":     "progress_ttl 4m extra",
	}

	for name, directive := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseUploadConfig(t, "pogo_upload {\n\tstore default {\n\t\t"+directive+"\n\t}\n}")
			if err == nil {
				t.Fatal("expected extra argument error")
			}
			if !strings.Contains(err.Error(), "too many arguments") {
				t.Fatalf("expected too many arguments error, got %v", err)
			}
		})
	}
}

func TestValidateStoreConfigsRejectsDuplicateStore(t *testing.T) {
	err := validateStoreConfigs([]StoreConfig{
		validStoreConfig(defaultStoreName),
		validStoreConfig(defaultStoreName),
	})
	if err == nil {
		t.Fatal("expected duplicate store error")
	}
}

func TestValidateStoreConfigsRequiresWorkerSecretAndRoot(t *testing.T) {
	tests := []StoreConfig{
		{Name: defaultStoreName, SigningSecret: "secret", Backend: BackendConfig{Type: "local", Root: "/tmp/uploads"}},
		{Name: defaultStoreName, Worker: "worker.php", Backend: BackendConfig{Type: "local", Root: "/tmp/uploads"}},
		{Name: defaultStoreName, Worker: "worker.php", SigningSecret: "secret", Backend: BackendConfig{Type: "local"}},
	}
	for _, cfg := range tests {
		if err := validateStoreConfigs([]StoreConfig{cfg}); err == nil {
			t.Fatalf("expected validation error for %#v", cfg)
		}
	}
}

func TestUploadHandlerUnmarshal(t *testing.T) {
	d := caddyfile.NewTestDispenser(`pogo_upload media`)
	h := &UploadHandler{}
	if err := h.UnmarshalCaddyfile(d); err != nil {
		t.Fatalf("unmarshal handler failed: %v", err)
	}
	if h.Store != "media" {
		t.Fatalf("unexpected store: %q", h.Store)
	}
}

func validStoreConfig(name string) StoreConfig {
	return StoreConfig{
		Name:          name,
		Worker:        "worker.php",
		SigningSecret: "secret",
		Backend: BackendConfig{
			Type: "local",
			Root: "/tmp/uploads",
		},
	}
}
