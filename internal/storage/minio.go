package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/your-org/fd/internal/config"
)

type MinIOStore struct {
	client *minio.Client
	bucket string
}

func NewMinIOStore(cfg config.MinIOConfig) (*MinIOStore, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}

	return &MinIOStore{
		client: client,
		bucket: cfg.Bucket,
	}, nil
}

// EnsureBucket creates the bucket if it doesn't exist.
func (s *MinIOStore) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check bucket: %w", err)
	}
	if !exists {
		if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("create bucket: %w", err)
		}
	}
	return nil
}

// PutObject uploads data to MinIO under the given key.
func (s *MinIOStore) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	reader := bytes.NewReader(data)
	_, err := s.client.PutObject(ctx, s.bucket, key, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("put object %s: %w", key, err)
	}
	return nil
}

// GetObject retrieves data from MinIO by key.
func (s *MinIOStore) GetObject(ctx context.Context, key string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object %s: %w", key, err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("read object %s: %w", key, err)
	}
	return data, nil
}

// DeleteObject removes an object from MinIO.
func (s *MinIOStore) DeleteObject(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

// Ping checks MinIO connectivity.
func (s *MinIOStore) Ping(ctx context.Context) error {
	_, err := s.client.BucketExists(ctx, s.bucket)
	return err
}
