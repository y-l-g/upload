package upload

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
)

func TestManagerProgressAndCancel(t *testing.T) {
	root := t.TempDir()
	m := newTestManager(t, root, &fakeWorkers{response: `{"ok":true}`, threads: 1})
	store := m.stores[defaultStoreName]

	ctx, cancel := context.WithCancel(context.Background())
	intent := uploadIntent{
		UploadID:  "upl_test",
		Store:     defaultStoreName,
		Key:       "avatar.txt",
		MaxBytes:  100,
		ExpiresAt: time.Unix(200, 0).UTC(),
	}
	if err := store.startProgress(intent, cancel, time.Unix(100, 0).UTC()); err != nil {
		t.Fatalf("start progress failed: %v", err)
	}
	store.addProgressBytes(intent.UploadID, 10)

	progressJSON, apiErr := m.progress(defaultStoreName, intent.UploadID)
	if apiErr != nil {
		t.Fatalf("progress failed: %v", apiErr)
	}
	var progress progressResponse
	if err := json.Unmarshal([]byte(progressJSON), &progress); err != nil {
		t.Fatalf("progress JSON invalid: %v", err)
	}
	if progress.State != progressReceiving || progress.BytesReceived != 10 {
		t.Fatalf("unexpected progress: %#v", progress)
	}

	cancelled, apiErr := m.cancelUpload(defaultStoreName, intent.UploadID)
	if apiErr != nil {
		t.Fatalf("cancel failed: %v", apiErr)
	}
	if !cancelled {
		t.Fatal("expected cancel to return true")
	}
	select {
	case <-ctx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("cancel did not cancel context")
	}
}

func TestManagerStatusFiltersStore(t *testing.T) {
	m := newTestManager(t, t.TempDir(), &fakeWorkers{response: `{"ok":true}`, threads: 3})
	statusJSON, apiErr := m.status(defaultStoreName)
	if apiErr != nil {
		t.Fatalf("status failed: %v", apiErr)
	}
	var status statusPayload
	if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
		t.Fatalf("status JSON invalid: %v", err)
	}
	if !status.Ready || len(status.Stores) != 1 || status.Stores[0].WorkerThreads != 3 {
		t.Fatalf("unexpected status: %#v", status)
	}
}

func TestStoreRuntimeUsedUploadReservationExpires(t *testing.T) {
	m := newTestManager(t, t.TempDir(), &fakeWorkers{response: `{"ok":true}`, threads: 1})
	store := m.stores[defaultStoreName]
	store.cfg.ProgressTTL = caddy.Duration(time.Second)
	now := time.Unix(100, 0).UTC()
	intent := uploadIntent{
		UploadID:  "upl_test",
		Store:     defaultStoreName,
		Key:       "avatar.txt",
		MaxBytes:  100,
		ExpiresAt: now.Add(10 * time.Second),
	}

	_, cancel := context.WithCancel(context.Background())
	if err := store.startProgress(intent, cancel, now); err != nil {
		t.Fatalf("start progress failed: %v", err)
	}
	store.finishProgress(intent.UploadID, progressCompleted, now)
	if progress := store.getProgress(intent.UploadID, now.Add(2*time.Second)); progress != nil {
		t.Fatalf("expected progress record to be cleaned up, got %#v", progress)
	}

	_, cancelBeforeExpiry := context.WithCancel(context.Background())
	if err := store.startProgress(intent, cancelBeforeExpiry, now.Add(2*time.Second)); err == nil {
		t.Fatal("expected used upload reservation to reject reuse before token expiry")
	}

	_, cancelAfterExpiry := context.WithCancel(context.Background())
	if err := store.startProgress(intent, cancelAfterExpiry, intent.ExpiresAt.Add(time.Second)); err != nil {
		t.Fatalf("expected reservation to expire after token expiry, got %v", err)
	}
}

func TestManagerUnknownStoreIsRuntimeError(t *testing.T) {
	m := newTestManager(t, t.TempDir(), &fakeWorkers{response: `{"ok":true}`})
	if _, apiErr := m.create("missing", `{"key":"avatar.txt","max_bytes":1}`); apiErr == nil || apiErr.kind != errKindRuntime {
		t.Fatalf("expected runtime error, got %v", apiErr)
	}
}
