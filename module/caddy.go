package upload

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	frankenphpCaddy "github.com/dunglas/frankenphp/caddy"
)

const (
	defaultStoreName       = "default"
	defaultTokenTTL        = 15 * time.Minute
	defaultMaxUploadBytes  = int64(1 << 30)
	defaultMaxConcurrency  = 32
	defaultReadTimeout     = 30 * time.Second
	defaultCompleteTimeout = 10 * time.Second
	defaultProgressTTL     = 10 * time.Minute
)

func init() {
	caddy.RegisterModule(Upload{})
	caddy.RegisterModule(UploadHandler{})
	httpcaddyfile.RegisterGlobalOption("pogo_upload", parseGlobalOption)
	httpcaddyfile.RegisterHandlerDirective("pogo_upload", parseHandlerDirective)
}

type Upload struct {
	Stores []StoreConfig `json:"stores,omitempty"`

	manager *manager
}

type StoreConfig struct {
	Name            string         `json:"name,omitempty"`
	Worker          string         `json:"worker,omitempty"`
	SigningSecret   string         `json:"signing_secret,omitempty"`
	Backend         BackendConfig  `json:"backend,omitempty"`
	TokenTTL        caddy.Duration `json:"token_ttl,omitempty"`
	MaxUploadBytes  int64          `json:"max_upload_bytes,omitempty"`
	MaxConcurrency  int            `json:"max_concurrency,omitempty"`
	ReadTimeout     caddy.Duration `json:"read_timeout,omitempty"`
	CompleteTimeout caddy.Duration `json:"complete_timeout,omitempty"`
	ProgressTTL     caddy.Duration `json:"progress_ttl,omitempty"`
}

type BackendConfig struct {
	Type string `json:"type,omitempty"`
	Root string `json:"root,omitempty"`
}

type UploadHandler struct {
	Store string `json:"store,omitempty"`

	manager *manager
}

func (Upload) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "pogo_upload",
		New: func() caddy.Module { return new(Upload) },
	}
}

func (UploadHandler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.pogo_upload",
		New: func() caddy.Module { return new(UploadHandler) },
	}
}

func (u *Upload) Provision(ctx caddy.Context) error {
	if err := validateStoreConfigs(u.Stores); err != nil {
		return err
	}

	metrics := NewMetrics(ctx.GetMetricsRegistry())
	stores := make(map[string]*storeRuntime, len(u.Stores))
	for _, cfg := range u.Stores {
		cfg = applyStoreDefaults(cfg)

		backend, err := newObjectStore(cfg)
		if err != nil {
			return fmt.Errorf("pogo_upload store %q backend: %w", cfg.Name, err)
		}

		workers := frankenphpCaddy.RegisterWorkers(
			"m#PogoUpload/"+cfg.Name,
			cfg.Worker,
			cfg.MaxConcurrency,
		)

		store := newStoreRuntime(cfg, backend, workers, ctx.Slogger(), metrics)
		stores[cfg.Name] = store
		metrics.SetConfig(cfg.Name, store.configSnapshot())
	}

	u.manager = newManager(stores)

	globalManagerMu.Lock()
	globalManager = u.manager
	globalManagerMu.Unlock()

	return nil
}

func (u *Upload) Cleanup() error {
	if u.manager != nil {
		u.manager.close()
	}

	globalManagerMu.Lock()
	if globalManager == u.manager {
		globalManager = nil
	}
	globalManagerMu.Unlock()

	return nil
}

func (u *Upload) Start() error {
	return nil
}

func (u *Upload) Stop() error {
	return u.Cleanup()
}

func (h *UploadHandler) Provision(ctx caddy.Context) error {
	app, err := ctx.App("pogo_upload")
	if err != nil {
		return fmt.Errorf("pogo_upload handler: %w", err)
	}
	uploadApp, ok := app.(*Upload)
	if !ok {
		return fmt.Errorf(`expected ctx.App("pogo_upload") to return *Upload, got %T`, app)
	}
	if uploadApp.manager == nil {
		return fmt.Errorf("pogo_upload handler: app manager is not provisioned")
	}

	if h.Store == "" {
		h.Store = defaultStoreName
	}
	if _, apiErr := uploadApp.manager.store(h.Store); apiErr != nil {
		return fmt.Errorf("pogo_upload handler store %q: %w", h.Store, apiErr)
	}
	h.manager = uploadApp.manager
	return nil
}

func (u *Upload) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "store":
				cfg, err := unmarshalStore(d)
				if err != nil {
					return err
				}
				u.Stores = append(u.Stores, cfg)
			default:
				return d.Errf(`unrecognized subdirective "%s"`, d.Val())
			}
		}
	}
	return nil
}

func (h *UploadHandler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		if !d.NextArg() {
			return d.ArgErr()
		}
		h.Store = d.Val()
		if d.NextArg() {
			return d.Errf(`too many arguments for "pogo_upload": %s`, d.Val())
		}
		if d.NextBlock(0) {
			return d.Err(`"pogo_upload" does not accept a block in a route`)
		}
	}

	if h.Store == "" {
		h.Store = defaultStoreName
	}
	return nil
}

func unmarshalStore(d *caddyfile.Dispenser) (StoreConfig, error) {
	cfg := StoreConfig{}
	if !d.NextArg() {
		return cfg, d.ArgErr()
	}
	cfg.Name = d.Val()
	if d.NextArg() {
		return cfg, d.Errf(`too many arguments for "store": %s`, d.Val())
	}

	for d.NextBlock(1) {
		switch d.Val() {
		case "worker":
			value, err := parseSingleArgDirective(d, "worker")
			if err != nil {
				return cfg, err
			}
			cfg.Worker = value
		case "signing_secret":
			value, err := parseSingleArgDirective(d, "signing_secret")
			if err != nil {
				return cfg, err
			}
			cfg.SigningSecret = value
		case "backend":
			value, err := parseSingleArgDirective(d, "backend")
			if err != nil {
				return cfg, err
			}
			cfg.Backend.Type = value
			nesting := d.Nesting()
			for d.NextBlock(nesting) {
				switch d.Val() {
				case "root":
					value, err := parseSingleArgDirective(d, "root")
					if err != nil {
						return cfg, err
					}
					cfg.Backend.Root = value
				default:
					return cfg, d.Errf(`unrecognized backend subdirective "%s"`, d.Val())
				}
			}
		case "token_ttl":
			value, err := parsePositiveDurationDirective(d, "token_ttl")
			if err != nil {
				return cfg, err
			}
			cfg.TokenTTL = caddy.Duration(value)
		case "max_upload_bytes":
			value, err := parsePositiveInt64Directive(d, "max_upload_bytes")
			if err != nil {
				return cfg, err
			}
			cfg.MaxUploadBytes = value
		case "max_concurrency":
			value, err := parsePositiveIntDirective(d, "max_concurrency")
			if err != nil {
				return cfg, err
			}
			cfg.MaxConcurrency = value
		case "read_timeout":
			value, err := parsePositiveDurationDirective(d, "read_timeout")
			if err != nil {
				return cfg, err
			}
			cfg.ReadTimeout = caddy.Duration(value)
		case "complete_timeout":
			value, err := parsePositiveDurationDirective(d, "complete_timeout")
			if err != nil {
				return cfg, err
			}
			cfg.CompleteTimeout = caddy.Duration(value)
		case "progress_ttl":
			value, err := parsePositiveDurationDirective(d, "progress_ttl")
			if err != nil {
				return cfg, err
			}
			cfg.ProgressTTL = caddy.Duration(value)
		default:
			return cfg, d.Errf(`unrecognized store subdirective "%s"`, d.Val())
		}
	}

	return cfg, nil
}

func validateStoreConfigs(configs []StoreConfig) error {
	if len(configs) == 0 {
		return fmt.Errorf("pogo_upload requires at least one store")
	}

	seen := make(map[string]struct{}, len(configs))
	for _, cfg := range configs {
		cfg = applyStoreDefaults(cfg)
		if cfg.Name == "" {
			return fmt.Errorf("pogo_upload store name is required")
		}
		if _, ok := seen[cfg.Name]; ok {
			return fmt.Errorf("duplicate pogo_upload store %q", cfg.Name)
		}
		seen[cfg.Name] = struct{}{}
		if cfg.Worker == "" {
			return fmt.Errorf("pogo_upload store %q worker is required", cfg.Name)
		}
		if cfg.SigningSecret == "" {
			return fmt.Errorf("pogo_upload store %q signing_secret is required", cfg.Name)
		}
		if cfg.Backend.Type != "local" {
			return fmt.Errorf("pogo_upload store %q unsupported backend %q", cfg.Name, cfg.Backend.Type)
		}
		if cfg.Backend.Root == "" {
			return fmt.Errorf("pogo_upload store %q local backend root is required", cfg.Name)
		}
		if cfg.MaxUploadBytes <= 0 {
			return fmt.Errorf("pogo_upload store %q max_upload_bytes must be positive", cfg.Name)
		}
		if cfg.MaxConcurrency <= 0 {
			return fmt.Errorf("pogo_upload store %q max_concurrency must be positive", cfg.Name)
		}
	}
	return nil
}

func applyStoreDefaults(cfg StoreConfig) StoreConfig {
	if cfg.Name == "" {
		cfg.Name = defaultStoreName
	}
	if cfg.Backend.Type == "" {
		cfg.Backend.Type = "local"
	}
	if cfg.TokenTTL <= 0 {
		cfg.TokenTTL = caddy.Duration(defaultTokenTTL)
	}
	if cfg.MaxUploadBytes <= 0 {
		cfg.MaxUploadBytes = defaultMaxUploadBytes
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = defaultMaxConcurrency
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = caddy.Duration(defaultReadTimeout)
	}
	if cfg.CompleteTimeout <= 0 {
		cfg.CompleteTimeout = caddy.Duration(defaultCompleteTimeout)
	}
	if cfg.ProgressTTL <= 0 {
		cfg.ProgressTTL = caddy.Duration(defaultProgressTTL)
	}
	return cfg
}

func newObjectStore(cfg StoreConfig) (objectStore, error) {
	switch cfg.Backend.Type {
	case "local":
		root := cfg.Backend.Root
		if !filepath.IsAbs(root) {
			abs, err := filepath.Abs(root)
			if err != nil {
				return nil, err
			}
			root = abs
		}
		if err := os.MkdirAll(root, 0o750); err != nil {
			return nil, err
		}
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, err
		}
		root = resolvedRoot
		return newLocalStore(root), nil
	default:
		return nil, fmt.Errorf("unsupported backend %q", cfg.Backend.Type)
	}
}

func parseSingleArgDirective(d *caddyfile.Dispenser, name string) (string, error) {
	if !d.NextArg() {
		return "", d.ArgErr()
	}
	value := d.Val()
	if d.NextArg() {
		return "", d.Errf(`too many arguments for "%s": %s`, name, d.Val())
	}
	return value, nil
}

func parsePositiveIntDirective(d *caddyfile.Dispenser, name string) (int, error) {
	raw, err := parseSingleArgDirective(d, name)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, d.Errf("failed to parse %s as a positive integer", name)
	}
	return value, nil
}

func parsePositiveInt64Directive(d *caddyfile.Dispenser, name string) (int64, error) {
	raw, err := parseSingleArgDirective(d, name)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, d.Errf("failed to parse %s as a positive integer", name)
	}
	return value, nil
}

func parsePositiveDurationDirective(d *caddyfile.Dispenser, name string) (time.Duration, error) {
	raw, err := parseSingleArgDirective(d, name)
	if err != nil {
		return 0, err
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, d.Errf("failed to parse %s as a positive duration", name)
	}
	return value, nil
}

func parseGlobalOption(d *caddyfile.Dispenser, _ any) (any, error) {
	app := &Upload{}
	if err := app.UnmarshalCaddyfile(d); err != nil {
		return nil, err
	}

	return httpcaddyfile.App{
		Name:  "pogo_upload",
		Value: caddyconfig.JSON(app, nil),
	}, nil
}

func parseHandlerDirective(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	handler := &UploadHandler{}
	err := handler.UnmarshalCaddyfile(h.Dispenser)
	return handler, err
}

func loggerOrDefault(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.Default()
}

var (
	_ caddy.Module                = (*Upload)(nil)
	_ caddy.App                   = (*Upload)(nil)
	_ caddy.Provisioner           = (*Upload)(nil)
	_ caddy.CleanerUpper          = (*Upload)(nil)
	_ caddyfile.Unmarshaler       = (*Upload)(nil)
	_ caddy.Module                = (*UploadHandler)(nil)
	_ caddy.Provisioner           = (*UploadHandler)(nil)
	_ caddyfile.Unmarshaler       = (*UploadHandler)(nil)
	_ caddyhttp.MiddlewareHandler = (*UploadHandler)(nil)
)
