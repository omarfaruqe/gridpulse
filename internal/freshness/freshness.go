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
