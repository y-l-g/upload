package upload

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
)

func TestCaddyfileRuntimeConfigAdaptsAndValidates(t *testing.T) {
	root := t.TempDir()
	caddyfileInput := fmt.Sprintf(`{
	frankenphp

	pogo_upload {
		store default {
			worker public/upload-worker.php
			signing_secret test-secret
			backend local {
				root %s
			}
		}
	}
}

:80 {
	route /_pogo/upload/* {
		pogo_upload default
	}

	php_server
}
`, root)

	adapter := caddyfile.Adapter{ServerType: httpcaddyfile.ServerType{}}
	adapted, _, err := adapter.Adapt([]byte(caddyfileInput), nil)
	if err != nil {
		t.Fatalf("adapt caddyfile: %v", err)
	}

	var cfg caddy.Config
	if err := json.Unmarshal(adapted, &cfg); err != nil {
		t.Fatalf("unmarshal adapted config: %v", err)
	}
	if err := caddy.Validate(&cfg); err != nil {
		t.Fatalf("validate adapted config: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(adapted, &raw); err != nil {
		t.Fatalf("unmarshal adapted json for assertions: %v", err)
	}

	apps, ok := raw["apps"].(map[string]any)
	if !ok {
		t.Fatalf("adapted config has no apps object: %s", adapted)
	}
	if _, ok := apps["pogo_upload"]; !ok {
		t.Fatalf("adapted config has no apps.pogo_upload object: %s", adapted)
	}
	if !jsonContainsKeyValue(raw, "handler", "pogo_upload") {
		t.Fatalf("adapted config has no pogo_upload HTTP handler: %s", adapted)
	}
}

func TestUploadProvisionSetsCurrentManagerAndCleanupClearsIt(t *testing.T) {
	app := &Upload{
		Stores: []StoreConfig{{
			Name:          defaultStoreName,
			Worker:        "public/upload-worker.php",
			SigningSecret: "test-secret",
			Backend: BackendConfig{
				Type: "local",
				Root: t.TempDir(),
			},
		}},
	}

	ctx, cancel := caddy.NewContext(caddy.Context{Context: context.Background()})
	defer cancel()

	if err := app.Provision(ctx); err != nil {
		t.Fatalf("provision upload app: %v", err)
	}
	if _, err := currentManager().store(defaultStoreName); err != nil {
		t.Fatalf("current manager cannot resolve default store: %v", err)
	}
	if err := app.Stop(); err != nil {
		t.Fatalf("stop upload app: %v", err)
	}
	if _, err := currentManager().store(defaultStoreName); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected stop to clear current manager, got %v", err)
	}
	if err := app.Cleanup(); err != nil {
		t.Fatalf("cleanup after stop failed: %v", err)
	}
}

func jsonContainsKeyValue(value any, key string, expected string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for k, v := range typed {
			if k == key {
				if text, ok := v.(string); ok && text == expected {
					return true
				}
			}
			if jsonContainsKeyValue(v, key, expected) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if jsonContainsKeyValue(item, key, expected) {
				return true
			}
		}
	}
	return false
}
