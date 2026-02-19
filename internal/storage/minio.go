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

// ListObjects returns all object keys under the given prefix, in the order MinIO returns them.
func (s *MinIOStore) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("list objects %s: %w", prefix, obj.Err)
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}

// DeleteObjects removes multiple objects from MinIO in a single batch request.
func (s *MinIOStore) DeleteObjects(ctx context.Context, keys []string) error {
	objectsCh := make(chan minio.ObjectInfo, len(keys))
	for _, key := range keys {
		objectsCh <- minio.ObjectInfo{Key: key}
	}
	close(objectsCh)
	for result := range s.client.RemoveObjects(ctx, s.bucket, objectsCh, minio.RemoveObjectsOptions{}) {
		if result.Err != nil {
			return fmt.Errorf("delete object %s: %w", result.ObjectName, result.Err)
		}
	}
	return nil
}

// Ping checks MinIO connectivity.
func (s *MinIOStore) Ping(ctx context.Context) error {
	_, err := s.client.BucketExists(ctx, s.bucket)
	return err
}
