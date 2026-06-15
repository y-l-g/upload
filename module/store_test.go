package upload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalStoreCommitWritesFinalObject(t *testing.T) {
	root := t.TempDir()
	store := newLocalStore(root)

	pending, err := store.Begin(context.Background(), "upl_test")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	if _, err := pending.Write([]byte("hello")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := pending.Commit(context.Background(), "users/123/avatar.txt", false); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "users", "123", "avatar.txt"))
	if err != nil {
		t.Fatalf("read final object failed: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("unexpected file content: %q", data)
	}
}

func TestLocalStoreCommitRejectsExistingObjectWhenOverwriteFalse(t *testing.T) {
	root := t.TempDir()
	store := newLocalStore(root)
	if err := os.WriteFile(filepath.Join(root, "avatar.txt"), []byte("old"), 0o640); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	pending, err := store.Begin(context.Background(), "upl_test")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	if _, err := pending.Write([]byte("new")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := pending.Commit(context.Background(), "avatar.txt", false); !errors.Is(err, errObjectExists) {
		t.Fatalf("expected object exists error, got %v", err)
	}
	_ = pending.Abort(context.Background())

	data, err := os.ReadFile(filepath.Join(root, "avatar.txt"))
	if err != nil {
		t.Fatalf("read final object failed: %v", err)
	}
	if string(data) != "old" {
		t.Fatalf("existing object was overwritten: %q", data)
	}
}

func TestSafeObjectPathRejectsTraversal(t *testing.T) {
	if _, err := safeObjectPath(t.TempDir(), "../avatar.txt"); err == nil {
		t.Fatal("expected traversal error")
	}
}

func TestLocalStoreCommitRejectsSymlinkAncestors(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}

	store := newLocalStore(root)
	pending, err := store.Begin(context.Background(), "upl_test")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	if _, err := pending.Write([]byte("hello")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := pending.Commit(context.Background(), "link/avatar.txt", false); !errors.Is(err, errObjectPath) {
		t.Fatalf("expected object path error, got %v", err)
	}
	_ = pending.Abort(context.Background())
}

func TestLocalStoreCommitRejectsSymlinkTargetWhenOverwriteTrue(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o640); err != nil {
		t.Fatalf("seed outside file failed: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "avatar.txt")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}

	store := newLocalStore(root)
	pending, err := store.Begin(context.Background(), "upl_test")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	if _, err := pending.Write([]byte("new")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := pending.Commit(context.Background(), "avatar.txt", true); !errors.Is(err, errObjectPath) {
		t.Fatalf("expected object path error, got %v", err)
	}
	_ = pending.Abort(context.Background())

	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside file failed: %v", err)
	}
	if string(data) != "outside" {
		t.Fatalf("outside file was overwritten: %q", data)
	}
}

func TestLocalStoreCommitRejectsSymlinkTargetWhenOverwriteFalse(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o640); err != nil {
		t.Fatalf("seed outside file failed: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "avatar.txt")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}

	store := newLocalStore(root)
	pending, err := store.Begin(context.Background(), "upl_test")
	if err != nil {
		t.Fatalf("begin failed: %v", err)
	}
	if _, err := pending.Write([]byte("new")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := pending.Commit(context.Background(), "avatar.txt", false); !errors.Is(err, errObjectExists) {
		t.Fatalf("expected object exists error, got %v", err)
	}
	_ = pending.Abort(context.Background())

	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside file failed: %v", err)
	}
	if string(data) != "outside" {
		t.Fatalf("outside file was overwritten: %q", data)
	}
}
