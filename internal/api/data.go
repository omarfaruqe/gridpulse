package api

import "net/http"

// iso formats a timestamptz column as an RFC 3339 UTC string in SQL, so the
// output never depends on the time zone of the machine the API runs on (the
// driver returns timestamps in the host's local zone). The strings have a fixed
// width, so ORDER BY 1 on such a column is chronological order.
func iso(column string) string {
	return "to_char(" + column + " AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"')"
}

// Row types: db tags are the column names, json tags the public field names.
// Nullable columns are pointers so a missing value is JSON null, never 0.

type Province struct {
	Code         string `db:"province_code" json:"code"`
	NameEN       string `db:"name_en" json:"name_en"`
	NameFR       string `db:"name_fr" json:"name_fr"`
	GridOperator string `db:"grid_operator" json:"grid_operator"`
}

type Fuel struct {
	FuelType    string `db:"fuel_type" json:"fuel_type"`
	NameEN      string `db:"name_en" json:"name_en"`
	NameFR      string `db:"name_fr" json:"name_fr"`
	IsRenewable *bool  `db:"is_renewable" json:"is_renewable"`
	IsLowCarbon *bool  `db:"is_low_carbon" json:"is_low_carbon"`
}

type LoadPoint struct {
	HourUTC    string   `db:"hour_utc" json:"hour_utc"`
	LoadMW     *float64 `db:"load_mw" json:"load_mw"`
	IsComplete bool     `db:"is_complete" json:"is_complete"`
	Source     string   `db:"source" json:"source"`
}

type GenerationPoint struct {
	HourUTC       string   `db:"hour_utc" json:"hour_utc"`
	FuelType      string   `db:"fuel_type" json:"fuel_type"`
	GenerationMW  *float64 `db:"generation_mw" json:"generation_mw"`
	OutputQuality *int16   `db:"output_quality" json:"output_quality"`
	IsComplete    bool     `db:"is_complete" json:"is_complete"`
}

type PricePoint struct {
	HourUTC     string   `db:"hour_utc" json:"hour_utc"`
	PriceCADMWh *float64 `db:"price_cad_mwh" json:"price_cad_mwh"`
	PriceKind   string   `db:"price_kind" json:"price_kind"`
	Source      string   `db:"source" json:"source"`
}

type WeatherPoint struct {
	HourUTC           string   `db:"hour_utc" json:"hour_utc"`
	TempC             *float64 `db:"temp_c" json:"temp_c"`
	WindSpeedKMH      *float64 `db:"wind_speed_kmh" json:"wind_speed_kmh"`
	StationsReporting int64    `db:"stations_reporting" json:"stations_reporting"`
	StationsTotal     int64    `db:"stations_total" json:"stations_total"`
	IsComplete        bool     `db:"is_complete" json:"is_complete"`
}

type LoadWeatherPoint struct {
	HourUTC           string   `db:"hour_utc" json:"hour_utc"`
	LoadMW            float64  `db:"load_mw" json:"load_mw"`
	TempC             float64  `db:"temp_c" json:"temp_c"`
	WindSpeedKMH      *float64 `db:"wind_speed_kmh" json:"wind_speed_kmh"`
	StationsReporting int64    `db:"stations_reporting" json:"stations_reporting"`
	StationsTotal     int64    `db:"stations_total" json:"stations_total"`
}

type Reconciliation struct {
	Month          string   `db:"month" json:"month"`
	Group          string   `db:"comparison_group" json:"group"`
	IESOMWh        float64  `db:"ieso_mwh" json:"ieso_mwh"`
	StatCanMWh     float64  `db:"statcan_mwh" json:"statcan_mwh"`
	Ratio          *float64 `db:"ratio" json:"ratio"`
	IESOCoverage   float64  `db:"ieso_coverage" json:"ieso_coverage"`
	BaselineRatio  *float64 `db:"baseline_ratio" json:"baseline_ratio"`
	BaselineMonths int64    `db:"baseline_months" json:"baseline_months"`
	Deviation      *float64 `db:"deviation" json:"deviation"`
	Tolerance      *float64 `db:"tolerance" json:"tolerance"`
	Status         string   `db:"status" json:"status"`
}

func (a *API) provinces(w http.ResponseWriter, r *http.Request) {
	list[Province](a, w, r, map[string]any{},
		`SELECT province_code, name_en, name_fr, grid_operator FROM gold.dim_province ORDER BY province_code`)
}

func (a *API) fuels(w http.ResponseWriter, r *http.Request) {
	list[Fuel](a, w, r, map[string]any{},
		`SELECT fuel_type, name_en, name_fr, is_renewable, is_low_carbon FROM gold.dim_fuel_type ORDER BY fuel_type`)
}

// scope reads province and range and reports a bad request to the client itself.
func (a *API) scope(w http.ResponseWriter, r *http.Request) (string, span, map[string]any, bool) {
	q := r.URL.Query()
	prov, err := parseProvince(q)
	if err != nil {
		a.fail(w, r, err)
		return "", span{}, nil, false
	}
	rng, err := parseRange(q, a.Now())
	if err != nil {
		a.fail(w, r, err)
		return "", span{}, nil, false
	}
	meta := map[string]any{"province": prov, "from": rng.fromDay, "to": rng.lastDay, "timezone": "UTC"}
	return prov, rng, meta, true
}

func (a *API) load(w http.ResponseWriter, r *http.Request) {
	prov, rng, meta, ok := a.scope(w, r)
	if !ok {
		return
	}
	list[LoadPoint](a, w, r, meta, `
SELECT `+iso("hour_utc")+` AS hour_utc, load_mw, is_complete, source FROM gold.fact_hourly_load
WHERE province_code = $1 AND hour_utc >= $2 AND hour_utc < $3 ORDER BY 1`, prov, rng.from, rng.to)
}

func (a *API) generation(w http.ResponseWriter, r *http.Request) {
	prov, rng, meta, ok := a.scope(w, r)
	if !ok {
		return
	}
	if prov != "ON" {
		meta["note"] = "generation by fuel is only available for ON"
	}
	list[GenerationPoint](a, w, r, meta, `
SELECT `+iso("hour_utc")+` AS hour_utc, fuel_type, generation_mw, output_quality, is_complete FROM gold.fact_generation_by_fuel
WHERE province_code = $1 AND hour_utc >= $2 AND hour_utc < $3 ORDER BY 1, 2`, prov, rng.from, rng.to)
}

func (a *API) prices(w http.ResponseWriter, r *http.Request) {
	prov, rng, meta, ok := a.scope(w, r)
	if !ok {
		return
	}
	if prov == "QC" {
		meta["note"] = "no hourly price series for QC"
	}
	list[PricePoint](a, w, r, meta, `
SELECT `+iso("hour_utc")+` AS hour_utc, price_cad_mwh, price_kind, source FROM gold.fact_hourly_price
WHERE province_code = $1 AND hour_utc >= $2 AND hour_utc < $3 ORDER BY 1`, prov, rng.from, rng.to)
}

func (a *API) weather(w http.ResponseWriter, r *http.Request) {
	prov, rng, meta, ok := a.scope(w, r)
	if !ok {
		return
	}
	list[WeatherPoint](a, w, r, meta, `
SELECT `+iso("hour_utc")+` AS hour_utc, temp_c, wind_speed_kmh, stations_reporting, stations_total, is_complete FROM gold.fact_hourly_weather
WHERE province_code = $1 AND hour_utc >= $2 AND hour_utc < $3 ORDER BY 1`, prov, rng.from, rng.to)
}

func (a *API) loadWeather(w http.ResponseWriter, r *http.Request) {
	prov, rng, meta, ok := a.scope(w, r)
	if !ok {
		return
	}
	list[LoadWeatherPoint](a, w, r, meta, `
SELECT `+iso("hour_utc")+` AS hour_utc, load_mw, temp_c, wind_speed_kmh, stations_reporting, stations_total FROM gold.mart_load_weather
WHERE province_code = $1 AND hour_utc >= $2 AND hour_utc < $3 ORDER BY 1`, prov, rng.from, rng.to)
}

func (a *API) reconciliation(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	group, status := q.Get("group"), q.Get("status")
	if group != "" {
		if err := oneOf("group", group, groups); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if status != "" {
		if err := oneOf("status", status, reconStatus); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	meta := map[string]any{
		"province": "ON",
		"note":     "ratio = IESO hourly report summed per month / StatCan table 25-10-0015-01; compared with the group's own 12-month baseline, not with 1",
	}
	list[Reconciliation](a, w, r, meta, `
SELECT to_char(ref_month, 'YYYY-MM') AS month, comparison_group, ieso_mwh, statcan_mwh, ratio,
       ieso_coverage::float8 AS ieso_coverage, baseline_ratio, baseline_months, deviation,
       tolerance::float8 AS tolerance, status
FROM gold.recon_generation_monthly
WHERE ($1 = '' OR comparison_group = $1) AND ($2 = '' OR status = $2)
ORDER BY ref_month, comparison_group`, group, status)
}
