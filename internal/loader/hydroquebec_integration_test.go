//go:build integration

package loader

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/db/dbtest"
)

type memBronze map[string][]byte

func (m memBronze) Get(_ context.Context, key string) ([]byte, error) {
	b, ok := m[key]
	if !ok {
		return nil, fmt.Errorf("no such key %q", key)
	}
	return b, nil
}

func (m memBronze) List(_ context.Context, prefix string) ([]string, error) {
	var keys []string
	for k := range m {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func feed(entries ...string) []byte {
	return []byte(`{"details":[` + strings.Join(entries, ",") + `]}`)
}

func slot(date string, mw float64) string {
	return fmt.Sprintf(`{"date":%q,"valeurs":{"demandeTotal":%v}}`, date, mw)
}

func emptySlot(date string) string {
	return fmt.Sprintf(`{"date":%q,"valeurs":{}}`, date)
}

const (
	keyOld = "source=hydroquebec_demand/date=2026-11-07/demand_20261107T220000Z.json"
	keyNew = "source=hydroquebec_demand/date=2026-11-07/demand_20261107T230000Z.json"
)

var (
	slotA = time.Date(2026, 11, 6, 5, 0, 0, 0, time.UTC)  // 2026-11-06T00:00 Eastern
	slotB = time.Date(2026, 11, 6, 5, 15, 0, 0, time.UTC) // 2026-11-06T00:15 Eastern
)

func twoSnapshots() memBronze {
	return memBronze{
		keyOld: feed(slot("2026-11-06T00:00:00", 999), slot("2026-11-06T00:15:00", 50)),
		keyNew: feed(slot("2026-11-06T00:00:00", 100), emptySlot("2026-11-06T00:15:00")),
	}
}

func demandAt(t *testing.T, l *Loader, ts time.Time) (mw *float64, key string) {
	t.Helper()
	err := l.Pool.QueryRow(context.Background(),
		`SELECT demand_mw, bronze_key FROM staging.hq_demand_15min WHERE time_utc = $1`, ts).Scan(&mw, &key)
	if err != nil {
		t.Fatalf("select %v: %v", ts, err)
	}
	return mw, key
}

func countRows(t *testing.T, l *Loader, table string) int {
	t.Helper()
	var n int
	if err := l.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLoadIsIdempotentAndSkipsLoadedFiles(t *testing.T) {
	ctx := context.Background()
	l := &Loader{Pool: dbtest.New(t), Bronze: twoSnapshots()}

	res, err := l.LoadHydroQuebec(ctx, keyNew, false)
	if err != nil || res.Skipped || res.Rows != 2 {
		t.Fatalf("first load = %+v, %v", res, err)
	}
	res, err = l.LoadHydroQuebec(ctx, keyNew, false)
	if err != nil || !res.Skipped {
		t.Fatalf("second load = %+v, %v, want skipped", res, err)
	}
	res, err = l.LoadHydroQuebec(ctx, keyNew, true)
	if err != nil || res.Skipped {
		t.Fatalf("forced load = %+v, %v, want not skipped", res, err)
	}
	if n := countRows(t, l, "staging.hq_demand_15min"); n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
	if n := countRows(t, l, "staging.load_log"); n != 1 {
		t.Errorf("load_log rows = %d, want 1", n)
	}
}

func TestNewestSnapshotWinsInAnyLoadOrder(t *testing.T) {
	ctx := context.Background()
	for name, order := range map[string][]string{
		"old then new": {keyOld, keyNew},
		"new then old": {keyNew, keyOld},
	} {
		t.Run(name, func(t *testing.T) {
			l := &Loader{Pool: dbtest.New(t), Bronze: twoSnapshots()}
			for _, key := range order {
				if _, err := l.LoadHydroQuebec(ctx, key, false); err != nil {
					t.Fatal(err)
				}
			}
			mw, key := demandAt(t, l, slotA)
			if mw == nil || *mw != 100 || key != keyNew {
				t.Errorf("slot A = %v from %s, want 100 from the newer snapshot", mw, key)
			}
			if mw, _ := demandAt(t, l, slotB); mw != nil {
				t.Errorf("slot B = %v, want NULL (the newer snapshot has no value)", *mw)
			}
		})
	}
}

func TestLoadStoresTimeResolution(t *testing.T) {
	ctx := context.Background()
	key := "source=hydroquebec_demand/date=2026-11-01/demand_20261101T230000Z.json"
	l := &Loader{Pool: dbtest.New(t), Bronze: memBronze{key: feed(
		slot("2026-11-01T00:30:00", 1), slot("2026-11-01T01:30:00", 2), slot("2026-03-08T02:30:00", 3),
	)}}
	if _, err := l.LoadHydroQuebec(ctx, key, false); err != nil {
		t.Fatal(err)
	}
	want := map[time.Time]string{
		time.Date(2026, 11, 1, 4, 30, 0, 0, time.UTC): "unique",
		time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC): "ambiguous",
		time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC):  "nonexistent",
	}
	for ts, res := range want {
		var got string
		if err := l.Pool.QueryRow(ctx, `SELECT time_resolution FROM staging.hq_demand_15min WHERE time_utc = $1`, ts).Scan(&got); err != nil {
			t.Fatalf("%v: %v", ts, err)
		}
		if got != res {
			t.Errorf("%v resolution = %q, want %q", ts, got, res)
		}
	}
}

func TestBadFileRollsBackAndIsReported(t *testing.T) {
	ctx := context.Background()
	good := keyOld
	bad := "source=hydroquebec_demand/date=2026-11-07/demand_20261107T230000Z.json"
	l := &Loader{Pool: dbtest.New(t), Bronze: memBronze{
		good: feed(slot("2026-11-06T00:00:00", 1)),
		bad:  []byte(`{"details":[]}`), // schema drift: no slots
	}}
	results, err := l.LoadAllHydroQuebec(ctx, false)
	if err == nil {
		t.Fatal("expected the bad file to be reported")
	}
	if len(results) != 1 || results[0].Key != good {
		t.Errorf("results = %+v, want only the good file", results)
	}
	if n := countRows(t, l, "staging.load_log"); n != 1 {
		t.Errorf("load_log rows = %d, want 1 (bad file must not be logged)", n)
	}
}

func TestLoadAllSkipsOnSecondRun(t *testing.T) {
	ctx := context.Background()
	l := &Loader{Pool: dbtest.New(t), Bronze: twoSnapshots()}
	if res, err := l.LoadAllHydroQuebec(ctx, false); err != nil || len(res) != 2 {
		t.Fatalf("first run = %v, %v", res, err)
	}
	res, err := l.LoadAllHydroQuebec(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if !r.Skipped {
			t.Errorf("%s was not skipped on the second run", r.Key)
		}
	}
}
