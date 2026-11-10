// Package config loads GridPulse settings from environment variables.
package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds all runtime settings shared by the GridPulse binaries.
type Config struct {
	S3Endpoint  string // host:port, no scheme
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3UseSSL    bool

	PostgresDSN string

	AESOAPIKey string // secret: never log this

	// DisabledJobs: scheduler jobs to leave out, from GRIDPULSE_DISABLE_JOBS
	// (comma-separated names, for example "hydroquebec").
	DisabledJobs []string
}

// Load reads configuration from the environment. Defaults match the local
// docker-compose stack, so a fresh checkout works without a .env file.
func Load() Config {
	return Config{
		S3Endpoint:   getenv("GRIDPULSE_S3_ENDPOINT", "localhost:8333"),
		S3Bucket:     getenv("GRIDPULSE_S3_BUCKET", "bronze"),
		S3AccessKey:  getenv("GRIDPULSE_S3_ACCESS_KEY", "gridpulse"),
		S3SecretKey:  getenv("GRIDPULSE_S3_SECRET_KEY", "gridpulse-dev-secret"),
		S3UseSSL:     getenv("GRIDPULSE_S3_USE_SSL", "false") == "true",
		AESOAPIKey:   getenv("GRIDPULSE_AESO_API_KEY", ""),
		DisabledJobs: splitList(getenv("GRIDPULSE_DISABLE_JOBS", "")),
		PostgresDSN: getenv("GRIDPULSE_POSTGRES_DSN", fmt.Sprintf(
			"postgres://%s:%s@localhost:5433/%s?sslmode=disable",
			getenv("POSTGRES_USER", "gridpulse"),
			getenv("POSTGRES_PASSWORD", "gridpulse-dev-secret"),
			getenv("POSTGRES_DB", "gridpulse"),
		)),
	}
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// splitList splits a comma-separated list and drops blanks and spaces.
func splitList(v string) []string {
	var out []string
	for _, f := range strings.Split(v, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
