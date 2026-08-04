package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileDocumentStore is the production/local-compose encrypted object store.
// Callers seal bytes before Put; this adapter never receives plaintext.
type FileDocumentStore struct {
	root string
}

func NewFileDocumentStore(root string) (*FileDocumentStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("auth: object store directory is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("auth: resolve object store directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("auth: create object store directory: %w", err)
	}
	return &FileDocumentStore{root: absolute}, nil
}

func (s *FileDocumentStore) path(key string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(key))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrDocumentInvalid
	}
	return filepath.Join(s.root, clean), nil
}

func (s *FileDocumentStore) Put(_ context.Context, key string, content []byte, _ string) error {
	target, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".object-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

func (s *FileDocumentStore) Get(_ context.Context, key string) ([]byte, error) {
	target, err := s.path(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(target)
}

func (s *FileDocumentStore) Delete(_ context.Context, key string) error {
	target, err := s.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
