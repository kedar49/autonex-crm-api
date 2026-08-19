package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Driver interface {
	Save(ctx context.Context, key string, data io.Reader) (string, error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

type LocalStorage struct {
	baseDir string
}

func NewLocalStorage(baseDir string) (*LocalStorage, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("storage: failed to create base dir: %w", err)
	}
	return &LocalStorage{baseDir: baseDir}, nil
}

func (l *LocalStorage) Save(ctx context.Context, key string, data io.Reader) (string, error) {
	fullPath := filepath.Join(l.baseDir, key)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return "", err
	}

	f, err := os.Create(fullPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, data); err != nil {
		return "", err
	}

	return fullPath, nil
}

func (l *LocalStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	fullPath := filepath.Join(l.baseDir, key)
	return os.Open(fullPath)
}

func (l *LocalStorage) Delete(ctx context.Context, key string) error {
	fullPath := filepath.Join(l.baseDir, key)
	return os.Remove(fullPath)
}
