package hydroquebec

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// SnapshotFromKey recovers the upstream snapshot time from a bronze key such as
// source=hydroquebec_demand/date=2026-11-07/demand_20261107T224634Z.json.
func SnapshotFromKey(key string) (time.Time, error) {
	base := path.Base(key)
	if !strings.HasPrefix(base, "demand_") || !strings.HasSuffix(base, ".json") {
		return time.Time{}, fmt.Errorf("hydroquebec: unexpected bronze file name %q", base)
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(base, "demand_"), ".json")
	t, err := time.Parse("20060102T150405Z", stamp)
	if err != nil {
		return time.Time{}, fmt.Errorf("hydroquebec: bad snapshot time in %q: %w", base, err)
	}
	return t, nil
}
