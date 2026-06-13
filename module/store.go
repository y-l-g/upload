package upload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	errObjectExists = errors.New("object already exists")
	errObjectPath   = errors.New("object key resolves outside backend root")
)

type objectStore interface {
	Begin(context.Context, string) (pendingObject, error)
	Delete(context.Context, string) error
}

type pendingObject interface {
	io.WriteCloser
	Commit(context.Context, string, bool) error
	Abort(context.Context) error
}

type localStore struct {
	root string
	tmp  string
}

type localPendingObject struct {
	file *os.File
	tmp  string
	root string
	done bool
}

func newLocalStore(root string) *localStore {
	return &localStore{
		root: root,
		tmp:  filepath.Join(root, ".pogo-upload-tmp"),
	}
}

func (s *localStore) Begin(_ context.Context, uploadID string) (pendingObject, error) {
	if err := os.MkdirAll(s.tmp, 0o750); err != nil {
		return nil, err
	}

	file, err := os.CreateTemp(s.tmp, uploadID+"-*.part")
	if err != nil {
		return nil, err
	}

	return &localPendingObject{
		file: file,
		tmp:  file.Name(),
		root: s.root,
	}, nil
}

func (s *localStore) Delete(_ context.Context, key string) error {
	target, err := safeObjectPath(s.root, key)
	if err != nil {
		return err
	}
	if err := ensureNoSymlinkAncestors(s.root, key); err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (p *localPendingObject) Write(data []byte) (int, error) {
	if p.done {
		return 0, os.ErrClosed
	}
	return p.file.Write(data)
}

func (p *localPendingObject) Close() error {
	if p.file == nil {
		return nil
	}
	return p.file.Close()
}

func (p *localPendingObject) Commit(_ context.Context, key string, overwrite bool) error {
	if p.done {
		return os.ErrClosed
	}
	target, err := safeObjectPath(p.root, key)
	if err != nil {
		return err
	}
	if err := ensureNoSymlinkAncestors(p.root, key); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	if err := p.file.Close(); err != nil {
		return err
	}

	if overwrite {
		if err := os.Rename(p.tmp, target); err != nil {
			return err
		}
		p.done = true
		return nil
	}

	if err := os.Link(p.tmp, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errObjectExists
		}
		return err
	}
	p.done = true
	_ = os.Remove(p.tmp)
	return nil
}

func (p *localPendingObject) Abort(_ context.Context) error {
	if p.done {
		return nil
	}
	p.done = true
	if p.file != nil {
		_ = p.file.Close()
	}
	if err := os.Remove(p.tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func safeObjectPath(root string, key string) (string, error) {
	if err := validateObjectKey(key); err != nil {
		return "", err
	}
	target := filepath.Join(root, filepath.FromSlash(key))
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	cleanTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return "", err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %s", errObjectPath, key)
	}
	return cleanTarget, nil
}

func ensureNoSymlinkAncestors(root string, key string) error {
	parts := strings.Split(key, "/")
	current := root
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink ancestor in %s", errObjectPath, key)
		}
		if !info.IsDir() {
			return fmt.Errorf("%w: non-directory ancestor in %s", errObjectPath, key)
		}
	}
	return nil
}
