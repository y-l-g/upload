package upload

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dunglas/frankenphp"
)

const (
	progressReceiving = "receiving"
	progressCompleted = "completed"
	progressFailed    = "failed"
	progressCancelled = "cancelled"
)

var (
	globalManager   *manager
	globalManagerMu sync.RWMutex
)

type manager struct {
	stores map[string]*storeRuntime
	closed atomic.Bool
}

type storeRuntime struct {
	cfg     StoreConfig
	backend objectStore
	worker  frankenphp.Workers
	logger  *slog.Logger
	metrics *Metrics

	sem chan struct{}

	progressMu sync.Mutex
	progress   map[string]*progressRecord
	counters   storeCounters

	closed atomic.Bool
}

type progressRecord struct {
	UploadID      string    `json:"upload_id"`
	State         string    `json:"state"`
	BytesReceived int64     `json:"bytes_received"`
	MaxBytes      int64     `json:"max_bytes"`
	StartedAt     time.Time `json:"-"`
	FinishedAt    time.Time `json:"-"`
	cancel        context.CancelFunc
}

type progressResponse struct {
	UploadID      string `json:"upload_id"`
	State         string `json:"state"`
	BytesReceived int64  `json:"bytes_received"`
	MaxBytes      int64  `json:"max_bytes"`
	StartedAt     string `json:"started_at"`
}

type storeCounters struct {
	accepted            atomic.Uint64
	completed           atomic.Uint64
	failed              atomic.Uint64
	cancelled           atomic.Uint64
	rejectedTokens      atomic.Uint64
	expiredTokens       atomic.Uint64
	sizeLimitFailures   atomic.Uint64
	contentTypeFailures atomic.Uint64
	bytesReceived       atomic.Uint64
	backendWriteFailure atomic.Uint64
	workerEventFailure  atomic.Uint64
}

type statusPayload struct {
	Ready  bool          `json:"ready"`
	Stores []storeStatus `json:"stores"`
}

type storeStatus struct {
	Store                  string `json:"store"`
	Ready                  bool   `json:"ready"`
	TokenTTLSeconds        int64  `json:"token_ttl_seconds"`
	MaxUploadBytes         int64  `json:"max_upload_bytes"`
	MaxConcurrency         int    `json:"max_concurrency"`
	ReadTimeoutSeconds     int64  `json:"read_timeout_seconds"`
	CompleteTimeoutSeconds int64  `json:"complete_timeout_seconds"`
	ProgressTTLSeconds     int64  `json:"progress_ttl_seconds"`
	WorkerThreads          int    `json:"worker_threads"`
	ActiveUploads          int    `json:"active_uploads"`
	Accepted               uint64 `json:"accepted"`
	Completed              uint64 `json:"completed"`
	Failed                 uint64 `json:"failed"`
	Cancelled              uint64 `json:"cancelled"`
	RejectedTokens         uint64 `json:"rejected_tokens"`
	ExpiredTokens          uint64 `json:"expired_tokens"`
	SizeLimitFailures      uint64 `json:"size_limit_failures"`
	ContentTypeFailures    uint64 `json:"content_type_failures"`
	BytesReceived          uint64 `json:"bytes_received"`
	BackendWriteFailures   uint64 `json:"backend_write_failures"`
	WorkerEventFailures    uint64 `json:"worker_event_failures"`
}

type configMetricsSnapshot struct {
	TokenTTLSeconds        int64
	MaxUploadBytes         int64
	MaxConcurrency         int
	ReadTimeoutSeconds     int64
	CompleteTimeoutSeconds int64
	ProgressTTLSeconds     int64
}

func newManager(stores map[string]*storeRuntime) *manager {
	return &manager{stores: stores}
}

func newStoreRuntime(cfg StoreConfig, backend objectStore, workers frankenphp.Workers, logger *slog.Logger, metrics *Metrics) *storeRuntime {
	return &storeRuntime{
		cfg:      cfg,
		backend:  backend,
		worker:   workers,
		logger:   loggerOrDefault(logger),
		metrics:  metrics,
		sem:      make(chan struct{}, cfg.MaxConcurrency),
		progress: make(map[string]*progressRecord),
	}
}

func currentManager() *manager {
	globalManagerMu.RLock()
	m := globalManager
	globalManagerMu.RUnlock()
	return m
}

func (m *manager) close() {
	if m == nil || !m.closed.CompareAndSwap(false, true) {
		return
	}
	for _, store := range m.stores {
		store.close()
	}
}

func (s *storeRuntime) close() {
	if s == nil || !s.closed.CompareAndSwap(false, true) {
		return
	}

	s.progressMu.Lock()
	records := make([]*progressRecord, 0, len(s.progress))
	for _, record := range s.progress {
		if record.State == progressReceiving && record.cancel != nil {
			records = append(records, record)
		}
	}
	s.progressMu.Unlock()

	for _, record := range records {
		record.cancel()
	}
}

func (m *manager) store(name string) (*storeRuntime, *apiError) {
	if m == nil || m.closed.Load() {
		return nil, runtimeError("pogo_upload is not configured")
	}
	if name == "" {
		name = defaultStoreName
	}
	store := m.stores[name]
	if store == nil || store.closed.Load() {
		return nil, runtimeError("unknown pogo_upload store %q", name)
	}
	return store, nil
}

func (m *manager) create(storeName string, rawIntent string) (string, *apiError) {
	store, apiErr := m.store(storeName)
	if apiErr != nil {
		return "", apiErr
	}

	now := time.Now().UTC()
	intent, apiErr := parseIntent(rawIntent, store.cfg.Name, store.cfg.MaxUploadBytes, time.Duration(store.cfg.TokenTTL), now)
	if apiErr != nil {
		return "", apiErr
	}

	token, err := signIntent(intent, store.cfg.SigningSecret, now)
	if err != nil {
		return "", runtimeErrorWrap(err, "failed to sign upload intent")
	}

	headers := map[string]string{}
	if len(intent.ContentTypes) > 0 {
		headers["content-type"] = intent.ContentTypes[0]
	}

	response, err := json.Marshal(createResponse{
		UploadID:  intent.UploadID,
		Method:    "PUT",
		URL:       "/_pogo/upload/" + token,
		Headers:   headers,
		ExpiresAt: intent.ExpiresAt.Format(time.RFC3339),
		MaxBytes:  intent.MaxBytes,
	})
	if err != nil {
		return "", runtimeErrorWrap(err, "failed to encode upload response")
	}

	return string(response), nil
}

func (m *manager) progress(storeName, uploadID string) (string, *apiError) {
	if uploadID == "" {
		return "", valueError("upload id must not be empty")
	}
	store, apiErr := m.store(storeName)
	if apiErr != nil {
		return "", apiErr
	}

	record := store.getProgress(uploadID, time.Now().UTC())
	if record == nil {
		return "", nil
	}

	response, err := json.Marshal(progressResponse{
		UploadID:      record.UploadID,
		State:         record.State,
		BytesReceived: record.BytesReceived,
		MaxBytes:      record.MaxBytes,
		StartedAt:     record.StartedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return "", runtimeErrorWrap(err, "failed to encode upload progress")
	}

	return string(response), nil
}

func (m *manager) cancelUpload(storeName, uploadID string) (bool, *apiError) {
	if uploadID == "" {
		return false, valueError("upload id must not be empty")
	}
	store, apiErr := m.store(storeName)
	if apiErr != nil {
		return false, apiErr
	}
	return store.cancelProgress(uploadID, time.Now().UTC()), nil
}

func (m *manager) status(storeName string) (string, *apiError) {
	if m == nil || m.closed.Load() {
		return "", runtimeError("pogo_upload is not configured")
	}

	stores := make([]*storeRuntime, 0, len(m.stores))
	if storeName != "" {
		store, apiErr := m.store(storeName)
		if apiErr != nil {
			return "", apiErr
		}
		stores = append(stores, store)
	} else {
		for _, store := range m.stores {
			stores = append(stores, store)
		}
	}

	statuses := make([]storeStatus, 0, len(stores))
	now := time.Now().UTC()
	ready := true
	for _, store := range stores {
		stat := store.status(now)
		if !stat.Ready {
			ready = false
		}
		statuses = append(statuses, stat)
	}

	response, err := json.Marshal(statusPayload{Ready: ready, Stores: statuses})
	if err != nil {
		return "", runtimeErrorWrap(err, "failed to encode upload status")
	}
	return string(response), nil
}

func (s *storeRuntime) acquire() bool {
	if s.closed.Load() {
		return false
	}
	select {
	case s.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *storeRuntime) release() {
	select {
	case <-s.sem:
	default:
	}
}

func (s *storeRuntime) startProgress(intent uploadIntent, cancel context.CancelFunc, now time.Time) error {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	s.cleanupLocked(now)
	if _, exists := s.progress[intent.UploadID]; exists {
		return errObjectExists
	}
	s.progress[intent.UploadID] = &progressRecord{
		UploadID:  intent.UploadID,
		State:     progressReceiving,
		MaxBytes:  intent.MaxBytes,
		StartedAt: now,
		cancel:    cancel,
	}
	s.metrics.SetActive(s.cfg.Name, s.activeUploadsLocked())
	return nil
}

func (s *storeRuntime) getProgress(uploadID string, now time.Time) *progressRecord {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	s.cleanupLocked(now)
	record := s.progress[uploadID]
	if record == nil {
		return nil
	}
	copyRecord := *record
	return &copyRecord
}

func (s *storeRuntime) addProgressBytes(uploadID string, bytes int64) {
	if bytes <= 0 {
		return
	}
	s.counters.bytesReceived.Add(uint64(bytes))
	s.metrics.AddBytes(s.cfg.Name, bytes)

	s.progressMu.Lock()
	if record := s.progress[uploadID]; record != nil {
		record.BytesReceived += bytes
	}
	s.progressMu.Unlock()
}

func (s *storeRuntime) finishProgress(uploadID, state string, now time.Time) string {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	record := s.progress[uploadID]
	if record == nil {
		return ""
	}

	if record.State != progressReceiving {
		return record.State
	}

	record.State = state
	record.FinishedAt = now
	record.cancel = nil
	switch state {
	case progressCompleted:
		s.counters.completed.Add(1)
		s.metrics.IncCompleted(s.cfg.Name)
	case progressFailed:
		s.counters.failed.Add(1)
		s.metrics.IncFailed(s.cfg.Name)
	case progressCancelled:
		s.counters.cancelled.Add(1)
		s.metrics.IncCancelled(s.cfg.Name)
	}
	s.metrics.SetActive(s.cfg.Name, s.activeUploadsLocked())
	return record.State
}

func (s *storeRuntime) cancelProgress(uploadID string, now time.Time) bool {
	s.progressMu.Lock()
	record := s.progress[uploadID]
	if record == nil || record.State != progressReceiving {
		s.progressMu.Unlock()
		return false
	}
	cancel := record.cancel
	record.State = progressCancelled
	record.FinishedAt = now
	record.cancel = nil
	s.counters.cancelled.Add(1)
	s.metrics.IncCancelled(s.cfg.Name)
	s.metrics.SetActive(s.cfg.Name, s.activeUploadsLocked())
	s.progressMu.Unlock()

	if cancel != nil {
		cancel()
	}
	return true
}

func (s *storeRuntime) status(now time.Time) storeStatus {
	s.progressMu.Lock()
	s.cleanupLocked(now)
	active := s.activeUploadsLocked()
	s.progressMu.Unlock()

	workerThreads := 0
	if s.worker != nil {
		workerThreads = s.worker.NumThreads()
	}

	return storeStatus{
		Store:                  s.cfg.Name,
		Ready:                  !s.closed.Load(),
		TokenTTLSeconds:        int64(time.Duration(s.cfg.TokenTTL).Seconds()),
		MaxUploadBytes:         s.cfg.MaxUploadBytes,
		MaxConcurrency:         s.cfg.MaxConcurrency,
		ReadTimeoutSeconds:     int64(time.Duration(s.cfg.ReadTimeout).Seconds()),
		CompleteTimeoutSeconds: int64(time.Duration(s.cfg.CompleteTimeout).Seconds()),
		ProgressTTLSeconds:     int64(time.Duration(s.cfg.ProgressTTL).Seconds()),
		WorkerThreads:          workerThreads,
		ActiveUploads:          active,
		Accepted:               s.counters.accepted.Load(),
		Completed:              s.counters.completed.Load(),
		Failed:                 s.counters.failed.Load(),
		Cancelled:              s.counters.cancelled.Load(),
		RejectedTokens:         s.counters.rejectedTokens.Load(),
		ExpiredTokens:          s.counters.expiredTokens.Load(),
		SizeLimitFailures:      s.counters.sizeLimitFailures.Load(),
		ContentTypeFailures:    s.counters.contentTypeFailures.Load(),
		BytesReceived:          s.counters.bytesReceived.Load(),
		BackendWriteFailures:   s.counters.backendWriteFailure.Load(),
		WorkerEventFailures:    s.counters.workerEventFailure.Load(),
	}
}

func (s *storeRuntime) cleanupLocked(now time.Time) {
	ttl := time.Duration(s.cfg.ProgressTTL)
	for id, record := range s.progress {
		if record.State == progressReceiving || record.FinishedAt.IsZero() {
			continue
		}
		if now.Sub(record.FinishedAt) > ttl {
			delete(s.progress, id)
		}
	}
}

func (s *storeRuntime) activeUploadsLocked() int {
	active := 0
	for _, record := range s.progress {
		if record.State == progressReceiving {
			active++
		}
	}
	return active
}

func (s *storeRuntime) configSnapshot() configMetricsSnapshot {
	return configMetricsSnapshot{
		TokenTTLSeconds:        int64(time.Duration(s.cfg.TokenTTL).Seconds()),
		MaxUploadBytes:         s.cfg.MaxUploadBytes,
		MaxConcurrency:         s.cfg.MaxConcurrency,
		ReadTimeoutSeconds:     int64(time.Duration(s.cfg.ReadTimeout).Seconds()),
		CompleteTimeoutSeconds: int64(time.Duration(s.cfg.CompleteTimeout).Seconds()),
		ProgressTTLSeconds:     int64(time.Duration(s.cfg.ProgressTTL).Seconds()),
	}
}

func isAlreadyKnownUpload(err error) bool {
	return errors.Is(err, errObjectExists)
}
