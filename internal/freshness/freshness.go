// Package freshness compares the newest data point of every series with the
// limit in ops.freshness_sla.
package freshness

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Status is the freshness of one series.
type Status struct {
	Series     string
	LatestHour *time.Time    // nil when the series has no data at all
	Lag        time.Duration // now minus LatestHour; negative when the data runs ahead of the clock
	MaxLag     time.Duration
	Stale      bool
}

// Check evaluates every series in ops.freshness_sla as of now. A series with
// no data is stale. Only hours with an actual value count: an hour that exists
// but is NULL does not make a series fresh.
func Check(ctx context.Context, pool *pgxpool.Pool, now time.Time) ([]Status, error) {
	rows, err := pool.Query(ctx, `
WITH latest AS (
    SELECT 'load_' || province_code AS series,
           max(hour_utc) FILTER (WHERE load_mw IS NOT NULL) AS latest_hour
    FROM gold.fact_hourly_load GROUP BY province_code
    UNION ALL
    SELECT 'generation_' || province_code,
           max(hour_utc) FILTER (WHERE generation_mw IS NOT NULL)
    FROM gold.fact_generation_by_fuel GROUP BY province_code
    UNION ALL
    SELECT 'price_' || province_code,
           max(hour_utc) FILTER (WHERE price_cad_mwh IS NOT NULL)
    FROM gold.fact_hourly_price GROUP BY province_code
    UNION ALL
    SELECT 'weather_' || province_code,
           max(hour_utc) FILTER (WHERE temp_c IS NOT NULL)
    FROM gold.fact_hourly_weather GROUP BY province_code
)
SELECT s.series, l.latest_hour, extract(epoch FROM s.max_lag)::float8
FROM ops.freshness_sla s
LEFT JOIN latest l USING (series)
ORDER BY s.series`)
	if err != nil {
		return nil, fmt.Errorf("freshness: query: %w", err)
	}
	defer rows.Close()

	var out []Status
	for rows.Next() {
		var st Status
		var maxLagSeconds float64
		if err := rows.Scan(&st.Series, &st.LatestHour, &maxLagSeconds); err != nil {
			return nil, fmt.Errorf("freshness: scan: %w", err)
		}
		st.MaxLag = time.Duration(maxLagSeconds * float64(time.Second))
		if st.LatestHour == nil {
			st.Stale = true
		} else {
			st.Lag = now.Sub(*st.LatestHour)
			st.Stale = st.Lag > st.MaxLag
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Record stores the result of one check in ops.freshness_lag and prunes rows
// older than 90 days, so the limits can be tuned from what was observed.
func Record(ctx context.Context, pool *pgxpool.Pool, now time.Time, statuses []Status) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("freshness: record: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	for _, s := range statuses {
		var lag *int64 // NULL when the series has no data
		if s.LatestHour != nil {
			v := int64(s.Lag.Seconds())
			lag = &v
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO ops.freshness_lag (checked_at, series, latest_hour, lag_seconds, max_lag_seconds, stale)
VALUES ($1, $2, $3, $4, $5, $6)`,
			now, s.Series, s.LatestHour, lag, int64(s.MaxLag.Seconds()), s.Stale); err != nil {
			return fmt.Errorf("freshness: record %s: %w", s.Series, err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ops.freshness_lag WHERE checked_at < $1::timestamptz - interval '90 days'`, now); err != nil {
		return fmt.Errorf("freshness: prune: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("freshness: record: commit: %w", err)
	}
	return nil
}

// WorstLags returns, per series, the largest lag seen at any check in the
// seven days before now. Series with no recorded data are absent.
func WorstLags(ctx context.Context, pool *pgxpool.Pool, now time.Time) (map[string]int64, error) {
	rows, err := pool.Query(ctx, `
SELECT series, max(lag_seconds)::bigint
FROM ops.freshness_lag
WHERE checked_at > $1::timestamptz - interval '7 days' AND checked_at <= $1 AND lag_seconds IS NOT NULL
GROUP BY series`, now)
	if err != nil {
		return nil, fmt.Errorf("freshness: worst lags: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var series string
		var worst int64
		if err := rows.Scan(&series, &worst); err != nil {
			return nil, fmt.Errorf("freshness: worst lags: scan: %w", err)
		}
		out[series] = worst
	}
	return out, rows.Err()
}
