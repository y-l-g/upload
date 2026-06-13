package upload

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type fakeWorkers struct {
	response any
	err      error
	delay    time.Duration
	threads  int

	calls      atomic.Int64
	messagesMu sync.Mutex
	messages   []string
}

func (f *fakeWorkers) SendRequest(http.ResponseWriter, *http.Request) error {
	return nil
}

func (f *fakeWorkers) SendMessage(ctx context.Context, message any, _ http.ResponseWriter) (any, error) {
	f.calls.Add(1)
	if text, ok := message.(string); ok {
		f.messagesMu.Lock()
		f.messages = append(f.messages, text)
		f.messagesMu.Unlock()
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func (f *fakeWorkers) NumThreads() int {
	return f.threads
}

func (f *fakeWorkers) lastMessage() string {
	f.messagesMu.Lock()
	defer f.messagesMu.Unlock()
	if len(f.messages) == 0 {
		return ""
	}
	return f.messages[len(f.messages)-1]
}

func newTestManager(t testFataler, root string, worker *fakeWorkers) *manager {
	t.Helper()
	cfg := applyStoreDefaults(StoreConfig{
		Name:          defaultStoreName,
		Worker:        "upload-worker.php",
		SigningSecret: "test-secret",
		Backend: BackendConfig{
			Type: "local",
			Root: root,
		},
		MaxUploadBytes: 1024 * 1024,
		MaxConcurrency: 2,
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := newStoreRuntime(cfg, newLocalStore(root), worker, logger, NewMetrics(nil))
	return newManager(map[string]*storeRuntime{defaultStoreName: store})
}

type testFataler interface {
	Helper()
}
