package bronze

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/omarfaruqe/gridpulse/internal/config"
)

// Store writes raw source files to an S3-compatible bucket.
type Store struct {
	client *minio.Client
	bucket string
}

// New connects to the object store and creates the bucket if it is missing.
func New(ctx context.Context, cfg config.Config) (*Store, error) {
	client, err := minio.New(cfg.S3Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.S3AccessKey, cfg.S3SecretKey, ""),
		Secure: cfg.S3UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("bronze: create client: %w", err)
	}
	s := &Store{client: client, bucket: cfg.S3Bucket}
	if err := s.ensureBucket(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) ensureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("bronze: check bucket %q: %w", s.bucket, err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("bronze: create bucket %q: %w", s.bucket, err)
	}
	return nil
}

// Put writes data under source=<source>/date=<date>/<filename> and returns the key.
// Writing the same source, date and filename again replaces the object, so
// re-runs and backfills are idempotent.
func (s *Store) Put(ctx context.Context, source string, date time.Time, filename, contentType string, data []byte) (string, error) {
	key, err := Key(source, date, filename)
	if err != nil {
		return "", err
	}
	_, err = s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return "", fmt.Errorf("bronze: put %q: %w", key, err)
	}
	return key, nil
}

// Get reads an object back by key.
func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("bronze: get %q: %w", key, err)
	}
	defer obj.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(obj); err != nil {
		return nil, fmt.Errorf("bronze: read %q: %w", key, err)
	}
	return buf.Bytes(), nil
}

// List returns the keys of all objects under the given prefix, in key order.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	for o := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if o.Err != nil {
			return nil, fmt.Errorf("bronze: list %q: %w", prefix, o.Err)
		}
		keys = append(keys, o.Key)
	}
	return keys, nil
}
