//go:build integration

package bronze

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/omarfaruqe/gridpulse/internal/config"
)

func TestPutIsIdempotent(t *testing.T) {
	ctx := context.Background()
	cfg := config.Load()
	cfg.S3Bucket = fmt.Sprintf("bronze-test-%d", time.Now().UnixNano())

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		for o := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Recursive: true}) {
			_ = s.client.RemoveObject(ctx, s.bucket, o.Key, minio.RemoveObjectOptions{})
		}
		_ = s.client.RemoveBucket(ctx, s.bucket)
	})

	date := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)

	key, err := s.Put(ctx, "hydroquebec", date, "demand.json", "application/json", []byte(`{"v":1}`))
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	if want := "source=hydroquebec/date=2026-11-07/demand.json"; key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}

	got, err := s.Get(ctx, key)
	if err != nil || string(got) != `{"v":1}` {
		t.Fatalf("first Get = %q, %v", got, err)
	}

	// Same source, date and filename: must replace, not duplicate.
	if _, err := s.Put(ctx, "hydroquebec", date, "demand.json", "application/json", []byte(`{"v":2}`)); err != nil {
		t.Fatalf("second Put: %v", err)
	}
	got, err = s.Get(ctx, key)
	if err != nil || string(got) != `{"v":2}` {
		t.Fatalf("second Get = %q, %v", got, err)
	}

	n := 0
	for o := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Recursive: true}) {
		if o.Err != nil {
			t.Fatalf("list: %v", o.Err)
		}
		n++
	}
	if n != 1 {
		t.Errorf("object count = %d, want 1", n)
	}
}
