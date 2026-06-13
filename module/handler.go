package upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

const uploadBufferSize = 64 * 1024

type uploadSuccessResponse struct {
	OK       bool   `json:"ok"`
	UploadID string `json:"upload_id"`
	Key      string `json:"key"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
}

type uploadErrorResponse struct {
	OK       bool             `json:"ok"`
	UploadID string           `json:"upload_id,omitempty"`
	Error    uploadErrorValue `json:"error"`
}

type uploadErrorValue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type uploadFailure struct {
	status  int
	code    string
	message string
	reason  string
	err     error
}

func (h *UploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	m := currentManager()
	store, apiErr := m.store(h.Store)
	if apiErr != nil {
		writeUploadError(w, http.StatusServiceUnavailable, "", "unavailable", apiErr.Error())
		return nil
	}

	if r.Method != http.MethodPut {
		w.Header().Set("Allow", http.MethodPut)
		writeUploadError(w, http.StatusMethodNotAllowed, "", "method_not_allowed", "upload endpoint only accepts PUT")
		return nil
	}

	token := tokenFromPath(r.URL.Path)
	if token == "" {
		writeUploadError(w, http.StatusBadRequest, "", "malformed_request", "upload token is required")
		return nil
	}

	intent, err := verifyToken(token, store.cfg.SigningSecret, time.Now().UTC())
	if err != nil {
		if errors.Is(err, errTokenExpired) {
			store.counters.expiredTokens.Add(1)
			store.metrics.IncExpiredToken(store.cfg.Name)
		} else {
			store.counters.rejectedTokens.Add(1)
			store.metrics.IncRejectedToken(store.cfg.Name)
		}
		writeUploadError(w, http.StatusUnauthorized, "", "invalid_token", tokenErrorMessage(err))
		return nil
	}

	if intent.Store != store.cfg.Name {
		store.counters.rejectedTokens.Add(1)
		store.metrics.IncRejectedToken(store.cfg.Name)
		writeUploadError(w, http.StatusUnauthorized, intent.UploadID, "invalid_token", "upload token is not valid for this store")
		return nil
	}

	if failure := validateUploadRequest(r, store, intent); failure != nil {
		startedAt := time.Now().UTC()
		store.handleFailure(intent, failure, 0, startedAt)
		writeUploadError(w, failure.status, intent.UploadID, failure.code, failure.message)
		return nil
	}

	if !store.acquire() {
		writeUploadError(w, http.StatusServiceUnavailable, intent.UploadID, "concurrency_limit", "store upload concurrency limit reached")
		return nil
	}
	defer store.release()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	body := r.Body
	cancelUpload := func() {
		cancel()
		_ = body.Close()
	}

	startedAt := time.Now().UTC()
	if err := store.startProgress(intent, cancelUpload, startedAt); err != nil {
		writeUploadError(w, http.StatusConflict, intent.UploadID, "conflict", "upload token has already been used")
		return nil
	}

	store.counters.accepted.Add(1)
	store.metrics.IncAccepted(store.cfg.Name)

	result, failure := store.receive(ctx, body, intent, contentTypeForEvent(r), startedAt)
	if failure != nil {
		store.handleFailure(intent, failure, result.bytes, startedAt)
		writeUploadError(w, failure.status, intent.UploadID, failure.code, failure.message)
		return nil
	}

	writeJSON(w, http.StatusOK, uploadSuccessResponse{
		OK:       true,
		UploadID: intent.UploadID,
		Key:      intent.Key,
		Bytes:    result.bytes,
		SHA256:   result.checksum,
	})
	return nil
}

type receiveResult struct {
	bytes    int64
	checksum string
}

func (s *storeRuntime) receive(ctx context.Context, body io.ReadCloser, intent uploadIntent, contentType string, startedAt time.Time) (receiveResult, *uploadFailure) {
	pending, err := s.backend.Begin(ctx, intent.UploadID)
	if err != nil {
		s.counters.backendWriteFailure.Add(1)
		s.metrics.IncBackendFailure(s.cfg.Name)
		s.finishProgress(intent.UploadID, progressFailed, time.Now().UTC())
		return receiveResult{}, &uploadFailure{status: http.StatusInternalServerError, code: "backend_error", message: "failed to start backend write", reason: "backend_error", err: err}
	}
	defer func() {
		_ = pending.Abort(context.Background())
	}()

	hasher := sha256.New()
	buffer := make([]byte, uploadBufferSize)
	var received int64

	timer := time.AfterFunc(time.Duration(s.cfg.ReadTimeout), func() {
		_ = body.Close()
	})
	defer timer.Stop()

	for {
		if ctx.Err() != nil {
			state := s.finishProgress(intent.UploadID, progressFailed, time.Now().UTC())
			reason := "client_aborted"
			status := 499
			message := "upload was interrupted"
			if state == progressCancelled {
				reason = "cancelled"
				message = "upload was cancelled"
			}
			return receiveResult{bytes: received}, &uploadFailure{status: status, code: reason, message: message, reason: reason, err: ctx.Err()}
		}

		n, readErr := body.Read(buffer)
		timer.Reset(time.Duration(s.cfg.ReadTimeout))
		if n > 0 {
			received += int64(n)
			s.addProgressBytes(intent.UploadID, int64(n))
			if received > intent.MaxBytes || received > s.cfg.MaxUploadBytes {
				s.counters.sizeLimitFailures.Add(1)
				s.metrics.IncSizeLimit(s.cfg.Name)
				s.finishProgress(intent.UploadID, progressFailed, time.Now().UTC())
				return receiveResult{bytes: received}, &uploadFailure{status: http.StatusRequestEntityTooLarge, code: "too_large", message: "upload exceeded max_bytes", reason: "too_large"}
			}
			if _, err := hasher.Write(buffer[:n]); err != nil {
				s.finishProgress(intent.UploadID, progressFailed, time.Now().UTC())
				return receiveResult{bytes: received}, &uploadFailure{status: http.StatusInternalServerError, code: "checksum_error", message: "failed to compute checksum", reason: "checksum_error", err: err}
			}
			if _, err := pending.Write(buffer[:n]); err != nil {
				s.counters.backendWriteFailure.Add(1)
				s.metrics.IncBackendFailure(s.cfg.Name)
				s.finishProgress(intent.UploadID, progressFailed, time.Now().UTC())
				return receiveResult{bytes: received}, &uploadFailure{status: http.StatusInternalServerError, code: "backend_error", message: "failed to write upload", reason: "backend_error", err: err}
			}
		}

		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			state := s.finishProgress(intent.UploadID, progressFailed, time.Now().UTC())
			reason := "client_aborted"
			status := 499
			message := "upload was interrupted"
			if state == progressCancelled {
				reason = "cancelled"
				message = "upload was cancelled"
			}
			if strings.Contains(strings.ToLower(readErr.Error()), "closed") && ctx.Err() == nil {
				reason = "read_timeout"
				status = http.StatusRequestTimeout
				message = "upload body read timed out"
			}
			return receiveResult{bytes: received}, &uploadFailure{status: status, code: reason, message: message, reason: reason, err: readErr}
		}
	}

	if err := pending.Commit(ctx, intent.Key, intent.Overwrite); err != nil {
		s.finishProgress(intent.UploadID, progressFailed, time.Now().UTC())
		if errors.Is(err, errObjectExists) {
			return receiveResult{bytes: received}, &uploadFailure{status: http.StatusConflict, code: "conflict", message: "object already exists", reason: "conflict", err: err}
		}
		s.counters.backendWriteFailure.Add(1)
		s.metrics.IncBackendFailure(s.cfg.Name)
		return receiveResult{bytes: received}, &uploadFailure{status: http.StatusInternalServerError, code: "backend_error", message: "failed to commit upload", reason: "backend_error", err: err}
	}

	checksum := hex.EncodeToString(hasher.Sum(nil))
	completedAt := time.Now().UTC()
	workerCtx, cancel := context.WithTimeout(context.Background(), time.Duration(s.cfg.CompleteTimeout))
	defer cancel()
	if err := s.sendCompletedEvent(workerCtx, intent, contentType, received, checksum, startedAt, completedAt); err != nil {
		s.counters.workerEventFailure.Add(1)
		s.metrics.IncWorkerFailure(s.cfg.Name)
		_ = s.backend.Delete(context.Background(), intent.Key)
		s.finishProgress(intent.UploadID, progressFailed, time.Now().UTC())
		return receiveResult{bytes: received}, &uploadFailure{status: http.StatusBadGateway, code: "worker_failed", message: "upload completion handler failed", reason: "worker_failed", err: err}
	}

	s.finishProgress(intent.UploadID, progressCompleted, completedAt)
	return receiveResult{bytes: received, checksum: checksum}, nil
}

func (s *storeRuntime) handleFailure(intent uploadIntent, failure *uploadFailure, bytesReceived int64, startedAt time.Time) {
	if failure == nil {
		return
	}
	if failure.err != nil {
		s.logger.Error("pogo_upload: upload failed", slog.String("store", s.cfg.Name), slog.String("upload_id", intent.UploadID), slog.String("reason", failure.reason), slog.Any("error", failure.err))
	}

	eventCtx, cancel := context.WithTimeout(context.Background(), time.Duration(s.cfg.CompleteTimeout))
	defer cancel()
	if err := s.sendFailedEvent(eventCtx, intent, failure.reason, bytesReceived, startedAt, time.Now().UTC()); err != nil {
		s.counters.workerEventFailure.Add(1)
		s.metrics.IncWorkerFailure(s.cfg.Name)
		s.logger.Error("pogo_upload: failed event handler failed", slog.String("store", s.cfg.Name), slog.String("upload_id", intent.UploadID), slog.String("reason", failure.reason), slog.Any("error", err))
	}

}

func validateUploadRequest(r *http.Request, store *storeRuntime, intent uploadIntent) *uploadFailure {
	if r.ContentLength > intent.MaxBytes || r.ContentLength > store.cfg.MaxUploadBytes {
		store.counters.sizeLimitFailures.Add(1)
		store.metrics.IncSizeLimit(store.cfg.Name)
		return &uploadFailure{status: http.StatusRequestEntityTooLarge, code: "too_large", message: "upload exceeded max_bytes", reason: "too_large"}
	}

	if len(intent.ContentTypes) == 0 {
		return nil
	}

	requestType, err := normalizeRequestContentType(r.Header.Get("Content-Type"))
	if err != nil {
		store.counters.contentTypeFailures.Add(1)
		store.metrics.IncContentTypeFailure(store.cfg.Name)
		return &uploadFailure{status: http.StatusUnsupportedMediaType, code: "unsupported_content_type", message: "content type is not accepted", reason: "unsupported_content_type"}
	}

	for _, accepted := range intent.ContentTypes {
		if requestType == accepted {
			return nil
		}
	}

	store.counters.contentTypeFailures.Add(1)
	store.metrics.IncContentTypeFailure(store.cfg.Name)
	return &uploadFailure{status: http.StatusUnsupportedMediaType, code: "unsupported_content_type", message: "content type is not accepted", reason: "unsupported_content_type"}
}

func normalizeRequestContentType(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("missing content type")
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return "", err
	}
	return normalizeContentType(mediaType)
}

func contentTypeForEvent(r *http.Request) string {
	contentType, err := normalizeRequestContentType(r.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return contentType
}

func tokenFromPath(path string) string {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return ""
	}
	idx := strings.LastIndex(path, "/")
	if idx < 0 || idx == len(path)-1 {
		return ""
	}
	return path[idx+1:]
}

func writeUploadError(w http.ResponseWriter, status int, uploadID string, code string, message string) {
	writeJSON(w, status, uploadErrorResponse{
		OK:       false,
		UploadID: uploadID,
		Error: uploadErrorValue{
			Code:    code,
			Message: message,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
}
