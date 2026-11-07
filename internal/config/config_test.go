package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("GRIDPULSE_S3_ENDPOINT", "")
	t.Setenv("GRIDPULSE_S3_BUCKET", "")
	cfg := Load()
	if cfg.S3Endpoint != "localhost:8333" {
		t.Errorf("S3Endpoint = %q, want localhost:8333", cfg.S3Endpoint)
	}
	if cfg.S3Bucket != "bronze" {
		t.Errorf("S3Bucket = %q, want bronze", cfg.S3Bucket)
	}
	if cfg.S3UseSSL {
		t.Error("S3UseSSL should default to false")
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("GRIDPULSE_S3_ENDPOINT", "s3.example.com:443")
	t.Setenv("GRIDPULSE_S3_USE_SSL", "true")
	cfg := Load()
	if cfg.S3Endpoint != "s3.example.com:443" {
		t.Errorf("S3Endpoint = %q", cfg.S3Endpoint)
	}
	if !cfg.S3UseSSL {
		t.Error("S3UseSSL should be true")
	}
}
