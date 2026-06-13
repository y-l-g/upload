package upload

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dunglas/frankenphp"
)

type uploadEvent struct {
	Type          string            `json:"type"`
	UploadID      string            `json:"upload_id"`
	Store         string            `json:"store"`
	Key           string            `json:"key"`
	Filename      string            `json:"filename,omitempty"`
	ContentType   string            `json:"content_type,omitempty"`
	Bytes         int64             `json:"bytes,omitempty"`
	SHA256        string            `json:"sha256,omitempty"`
	Reason        string            `json:"reason,omitempty"`
	BytesReceived int64             `json:"bytes_received,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	StartedAt     string            `json:"started_at"`
	CompletedAt   string            `json:"completed_at,omitempty"`
	FailedAt      string            `json:"failed_at,omitempty"`
}

type workerResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

func (s *storeRuntime) sendCompletedEvent(ctx context.Context, intent uploadIntent, contentType string, bytes int64, checksum string, startedAt time.Time, completedAt time.Time) error {
	event := uploadEvent{
		Type:        "completed",
		UploadID:    intent.UploadID,
		Store:       s.cfg.Name,
		Key:         intent.Key,
		Filename:    intent.Filename,
		ContentType: contentType,
		Bytes:       bytes,
		SHA256:      checksum,
		Metadata:    intent.Metadata,
		StartedAt:   startedAt.UTC().Format(time.RFC3339),
		CompletedAt: completedAt.UTC().Format(time.RFC3339),
	}
	return s.sendEvent(ctx, event)
}

func (s *storeRuntime) sendFailedEvent(ctx context.Context, intent uploadIntent, reason string, bytesReceived int64, startedAt time.Time, failedAt time.Time) error {
	event := uploadEvent{
		Type:          "failed",
		UploadID:      intent.UploadID,
		Store:         s.cfg.Name,
		Key:           intent.Key,
		Filename:      intent.Filename,
		Reason:        reason,
		BytesReceived: bytesReceived,
		Metadata:      intent.Metadata,
		StartedAt:     startedAt.UTC().Format(time.RFC3339),
		FailedAt:      failedAt.UTC().Format(time.RFC3339),
	}
	return s.sendEvent(ctx, event)
}

func (s *storeRuntime) sendEvent(ctx context.Context, event uploadEvent) error {
	if s.worker == nil {
		return fmt.Errorf("pogo_upload worker is unavailable")
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}

	response, err := s.worker.SendMessage(ctx, string(payload), nil)
	if err != nil {
		return err
	}

	normalized, err := normalizeWorkerResponse(response)
	if err != nil {
		return err
	}
	if !normalized.OK {
		if normalized.Error != "" {
			return fmt.Errorf("worker returned ok=false: %s", normalized.Error)
		}
		if normalized.Message != "" {
			return fmt.Errorf("worker returned ok=false: %s", normalized.Message)
		}
		return fmt.Errorf("worker returned ok=false")
	}

	return nil
}

func normalizeWorkerResponse(response any) (workerResponse, error) {
	switch value := response.(type) {
	case nil:
		return workerResponse{}, fmt.Errorf("worker returned an empty response")
	case string:
		return decodeWorkerResponse([]byte(value))
	case []byte:
		return decodeWorkerResponse(value)
	case map[string]any:
		data, err := json.Marshal(value)
		if err != nil {
			return workerResponse{}, fmt.Errorf("worker returned a non-JSON-compatible response: %w", err)
		}
		return decodeWorkerResponse(data)
	case frankenphp.AssociativeArray[any]:
		data, err := json.Marshal(value.Map)
		if err != nil {
			return workerResponse{}, fmt.Errorf("worker returned a non-JSON-compatible response: %w", err)
		}
		return decodeWorkerResponse(data)
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return workerResponse{}, fmt.Errorf("worker returned unsupported response type %T", response)
		}
		return decodeWorkerResponse(data)
	}
}

func decodeWorkerResponse(data []byte) (workerResponse, error) {
	var response workerResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return workerResponse{}, fmt.Errorf("worker returned invalid JSON: %w", err)
	}
	return response, nil
}
