// Package bronze writes raw source files to object storage.
package bronze

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// Key builds the object key for a raw file. Layout:
//
//	source=<source>/date=<YYYY-MM-DD>/<filename>
//
// The date is the data date (not the ingestion date) in UTC, so re-running
// the same source and date always targets the same key (idempotent).
func Key(source string, date time.Time, filename string) (string, error) {
	if source == "" || filename == "" {
		return "", fmt.Errorf("bronze: source and filename are required")
	}
	if strings.ContainsAny(source, "/=") || strings.Contains(filename, "/") {
		return "", fmt.Errorf("bronze: invalid characters in source %q or filename %q", source, filename)
	}
	d := date.UTC().Format("2006-01-02")
	return path.Join("source="+source, "date="+d, filename), nil
}
