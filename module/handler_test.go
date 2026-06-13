package upload

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUploadHandlerStoresFileAndSendsCompletedEvent(t *testing.T) {
	root := t.TempDir()
	worker := &fakeWorkers{response: `{"ok":true}`, threads: 1}
	m := newTestManager(t, root, worker)
	withGlobalManager(t, m)

	response := createTestIntent(t, m, `{
		"key":"users/123/avatar.jpg",
		"content_types":["image/jpeg"],
		"max_bytes":100,
		"metadata":{"user_id":"123"}
	}`)

	rec := performUpload(t, response.URL, "image/jpeg", "hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(root, "users", "123", "avatar.jpg"))
	if err != nil {
		t.Fatalf("stored file missing: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("unexpected stored file: %q", data)
	}

	var event uploadEvent
	if err := json.Unmarshal([]byte(worker.lastMessage()), &event); err != nil {
		t.Fatalf("worker event was not JSON: %v", err)
	}
	if event.Type != "completed" || event.UploadID != response.UploadID || event.Bytes != 5 {
		t.Fatalf("unexpected event: %#v", event)
	}
	if event.SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("unexpected checksum: %s", event.SHA256)
	}

	progress := m.stores[defaultStoreName].getProgress(response.UploadID, nowUTC())
	if progress == nil || progress.State != progressCompleted {
		t.Fatalf("unexpected progress: %#v", progress)
	}
}

func TestUploadHandlerRejectsWrongContentType(t *testing.T) {
	root := t.TempDir()
	worker := &fakeWorkers{response: `{"ok":true}`, threads: 1}
	m := newTestManager(t, root, worker)
	withGlobalManager(t, m)

	response := createTestIntent(t, m, `{"key":"avatar.jpg","content_types":["image/png"],"max_bytes":100}`)

	rec := performUpload(t, response.URL, "image/jpeg", "hello")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	if worker.calls.Load() != 1 {
		t.Fatalf("expected one failed event, got %d worker calls", worker.calls.Load())
	}
	var event uploadEvent
	if err := json.Unmarshal([]byte(worker.lastMessage()), &event); err != nil {
		t.Fatalf("worker event was not JSON: %v", err)
	}
	if event.Type != "failed" || event.Reason != "unsupported_content_type" {
		t.Fatalf("unexpected failed event: %#v", event)
	}
	if got := m.stores[defaultStoreName].counters.contentTypeFailures.Load(); got != 1 {
		t.Fatalf("unexpected content type failure count: %d", got)
	}
}

func TestUploadHandlerRejectsStreamingBodyAboveLimit(t *testing.T) {
	root := t.TempDir()
	worker := &fakeWorkers{response: `{"ok":true}`, threads: 1}
	m := newTestManager(t, root, worker)
	withGlobalManager(t, m)

	response := createTestIntent(t, m, `{"key":"avatar.txt","max_bytes":3}`)
	req := httptest.NewRequest(http.MethodPut, response.URL, strings.NewReader("hello"))
	req.ContentLength = -1
	rec := httptest.NewRecorder()

	if err := (&UploadHandler{Store: defaultStoreName}).ServeHTTP(rec, req, nil); err != nil {
		t.Fatalf("serve failed: %v", err)
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "avatar.txt")); !os.IsNotExist(err) {
		t.Fatalf("final object should not exist, stat err=%v", err)
	}
	if worker.calls.Load() != 1 {
		t.Fatalf("expected failed event, got %d worker calls", worker.calls.Load())
	}
	var event uploadEvent
	if err := json.Unmarshal([]byte(worker.lastMessage()), &event); err != nil {
		t.Fatalf("worker event was not JSON: %v", err)
	}
	if event.Type != "failed" || event.Reason != "too_large" {
		t.Fatalf("unexpected failed event: %#v", event)
	}
}

func TestUploadHandlerDeletesObjectWhenCompletedWorkerFails(t *testing.T) {
	root := t.TempDir()
	worker := &fakeWorkers{response: `{"ok":false,"error":"db down"}`, threads: 1}
	m := newTestManager(t, root, worker)
	withGlobalManager(t, m)

	response := createTestIntent(t, m, `{"key":"avatar.txt","max_bytes":100}`)
	rec := performUpload(t, response.URL, "application/octet-stream", "hello")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "avatar.txt")); !os.IsNotExist(err) {
		t.Fatalf("final object should have been deleted, stat err=%v", err)
	}
	progress := m.stores[defaultStoreName].getProgress(response.UploadID, nowUTC())
	if progress == nil || progress.State != progressFailed {
		t.Fatalf("unexpected progress: %#v", progress)
	}
}

func TestUploadHandlerRejectsTokenReuseWhileProgressKnown(t *testing.T) {
	root := t.TempDir()
	worker := &fakeWorkers{response: `{"ok":true}`, threads: 1}
	m := newTestManager(t, root, worker)
	withGlobalManager(t, m)

	response := createTestIntent(t, m, `{"key":"avatar.txt","max_bytes":100}`)
	first := performUpload(t, response.URL, "application/octet-stream", "hello")
	if first.Code != http.StatusOK {
		t.Fatalf("first upload failed: %d %s", first.Code, first.Body.String())
	}

	second := performUpload(t, response.URL, "application/octet-stream", "hello")
	if second.Code != http.StatusConflict {
		t.Fatalf("expected conflict, got %d %s", second.Code, second.Body.String())
	}
}

func createTestIntent(t *testing.T, m *manager, raw string) createResponse {
	t.Helper()
	responseJSON, apiErr := m.create(defaultStoreName, raw)
	if apiErr != nil {
		t.Fatalf("create failed: %v", apiErr)
	}
	var response createResponse
	if err := json.Unmarshal([]byte(responseJSON), &response); err != nil {
		t.Fatalf("create response was not JSON: %v", err)
	}
	return response
}

func performUpload(t *testing.T, target string, contentType string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	if err := (&UploadHandler{Store: defaultStoreName}).ServeHTTP(rec, req, nil); err != nil {
		t.Fatalf("serve failed: %v", err)
	}
	return rec
}

func withGlobalManager(t *testing.T, m *manager) {
	t.Helper()
	globalManagerMu.Lock()
	previous := globalManager
	globalManager = m
	globalManagerMu.Unlock()
	t.Cleanup(func() {
		globalManagerMu.Lock()
		globalManager = previous
		globalManagerMu.Unlock()
	})
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
