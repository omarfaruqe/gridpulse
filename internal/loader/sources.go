package loader

import (
	"github.com/omarfaruqe/gridpulse/internal/extractors/aeso"
	"github.com/omarfaruqe/gridpulse/internal/extractors/eccc"
	"github.com/omarfaruqe/gridpulse/internal/extractors/hydroquebec"
	"github.com/omarfaruqe/gridpulse/internal/extractors/ieso"
)

// sources maps a loader name to its bronze partition and parser. The partition
// names come from the extractors, so the two sides cannot drift apart.
var sources = map[string]source{
	"hydroquebec":     {hydroquebec.Source, parseHydroQuebec},
	"ieso-generation": {ieso.Source("GenOutputCapability"), parseGenerators},
	"ieso-demand":     {ieso.Source("Demand"), parseIESODemand},
	"ieso-price":      {ieso.Source("DAHourlyOntarioZonalPrice"), parseDAPrice},
	"aeso-price":      {aeso.Source("PoolPrice"), parsePoolPrice},
	"aeso-load":       {aeso.Source("AlbertaInternalLoad"), parseAESOLoad},
	"eccc":            {eccc.Source, parseECCC},
}

// nullable stores an empty string as NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// The WHERE clause keeps a replayed older snapshot from overwriting newer data,
// so backfills and re-runs in any order end in the same state.
const upsertHydroQuebec = `
INSERT INTO staging.hq_demand_15min (time_utc, local_time, demand_mw, time_resolution, snapshot_utc, bronze_key)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (time_utc) DO UPDATE SET
    local_time      = EXCLUDED.local_time,
    demand_mw       = EXCLUDED.demand_mw,
    time_resolution = EXCLUDED.time_resolution,
    snapshot_utc    = EXCLUDED.snapshot_utc,
    bronze_key      = EXCLUDED.bronze_key,
    loaded_at       = now()
WHERE EXCLUDED.snapshot_utc >= staging.hq_demand_15min.snapshot_utc`

func parseHydroQuebec(key string, data []byte) ([]statement, error) {
	obs, err := hydroquebec.Parse(data)
	if err != nil {
		return nil, err
	}
	snapshot, err := hydroquebec.SnapshotFromKey(key)
	if err != nil {
		return nil, err
	}
	stmts := make([]statement, 0, len(obs))
	for _, o := range obs {
		stmts = append(stmts, statement{upsertHydroQuebec,
			[]any{o.TimeUTC, o.LocalTime, o.DemandMW, o.Resolution.String(), snapshot, key}})
	}
	return stmts, nil
}

const upsertGenerator = `
INSERT INTO staging.ieso_generator_hourly (interval_start_utc, generator, fuel_type, hour_ending, energy_mw, bronze_key)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (interval_start_utc, generator) DO UPDATE SET
    fuel_type   = EXCLUDED.fuel_type,
    hour_ending = EXCLUDED.hour_ending,
    energy_mw   = EXCLUDED.energy_mw,
    bronze_key  = EXCLUDED.bronze_key,
    loaded_at   = now()`

func parseGenerators(key string, data []byte) ([]statement, error) {
	rows, err := ieso.ParseGenOutputCapability(data)
	if err != nil {
		return nil, err
	}
	stmts := make([]statement, 0, len(rows))
	for _, r := range rows {
		stmts = append(stmts, statement{upsertGenerator,
			[]any{r.IntervalStartUTC, r.Generator, r.Fuel, r.HourEnding, r.EnergyMW, key}})
	}
	return stmts, nil
}

const upsertIESODemand = `
INSERT INTO staging.ieso_demand_hourly (interval_start_utc, hour_ending, market_demand_mw, ontario_demand_mw, snapshot_utc, bronze_key)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (interval_start_utc) DO UPDATE SET
    hour_ending       = EXCLUDED.hour_ending,
    market_demand_mw  = EXCLUDED.market_demand_mw,
    ontario_demand_mw = EXCLUDED.ontario_demand_mw,
    snapshot_utc      = EXCLUDED.snapshot_utc,
    bronze_key        = EXCLUDED.bronze_key,
    loaded_at         = now()
WHERE EXCLUDED.snapshot_utc >= staging.ieso_demand_hourly.snapshot_utc`

func parseIESODemand(key string, data []byte) ([]statement, error) {
	rows, created, err := ieso.ParseDemand(data)
	if err != nil {
		return nil, err
	}
	snapshot := created.UTC()
	stmts := make([]statement, 0, len(rows))
	for _, r := range rows {
		stmts = append(stmts, statement{upsertIESODemand,
			[]any{r.IntervalStartUTC, r.HourEnding, r.MarketDemandMW, r.OntarioDemandMW, snapshot, key}})
	}
	return stmts, nil
}

const upsertDAPrice = `
INSERT INTO staging.ieso_da_price_hourly (interval_start_utc, hour_ending, zonal_price, loss_price, congestion_price, flag, bronze_key)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (interval_start_utc) DO UPDATE SET
    hour_ending      = EXCLUDED.hour_ending,
    zonal_price      = EXCLUDED.zonal_price,
    loss_price       = EXCLUDED.loss_price,
    congestion_price = EXCLUDED.congestion_price,
    flag             = EXCLUDED.flag,
    bronze_key       = EXCLUDED.bronze_key,
    loaded_at        = now()`

func parseDAPrice(key string, data []byte) ([]statement, error) {
	rows, err := ieso.ParseDAHourlyOntarioZonalPrice(data)
	if err != nil {
		return nil, err
	}
	stmts := make([]statement, 0, len(rows))
	for _, r := range rows {
		stmts = append(stmts, statement{upsertDAPrice,
			[]any{r.IntervalStartUTC, r.HourEnding, r.ZonalPrice, r.LossPrice, r.CongestionPrice, nullable(r.Flag), key}})
	}
	return stmts, nil
}

const upsertPoolPrice = `
INSERT INTO staging.aeso_pool_price_hourly (interval_start_utc, pool_price, forecast_pool_price, rolling_30day_avg, bronze_key)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (interval_start_utc) DO UPDATE SET
    pool_price          = EXCLUDED.pool_price,
    forecast_pool_price = EXCLUDED.forecast_pool_price,
    rolling_30day_avg   = EXCLUDED.rolling_30day_avg,
    bronze_key          = EXCLUDED.bronze_key,
    loaded_at           = now()`

func parsePoolPrice(key string, data []byte) ([]statement, error) {
	rows, err := aeso.ParsePoolPrice(data)
	if err != nil {
		return nil, err
	}
	stmts := make([]statement, 0, len(rows))
	for _, r := range rows {
		stmts = append(stmts, statement{upsertPoolPrice,
			[]any{r.StartUTC, r.PoolPrice, r.ForecastPoolPrice, r.Rolling30DayAvg, key}})
	}
	return stmts, nil
}

const upsertAESOLoad = `
INSERT INTO staging.aeso_load_hourly (interval_start_utc, load_mw, forecast_load_mw, bronze_key)
VALUES ($1, $2, $3, $4)
ON CONFLICT (interval_start_utc) DO UPDATE SET
    load_mw          = EXCLUDED.load_mw,
    forecast_load_mw = EXCLUDED.forecast_load_mw,
    bronze_key       = EXCLUDED.bronze_key,
    loaded_at        = now()`

func parseAESOLoad(key string, data []byte) ([]statement, error) {
	rows, err := aeso.ParseAlbertaInternalLoad(data)
	if err != nil {
		return nil, err
	}
	stmts := make([]statement, 0, len(rows))
	for _, r := range rows {
		stmts = append(stmts, statement{upsertAESOLoad,
			[]any{r.StartUTC, r.LoadMW, r.ForecastLoadMW, key}})
	}
	return stmts, nil
}

const upsertWeather = `
INSERT INTO staging.eccc_weather_hourly (
    climate_id, time_utc, station_name, province, temp, temp_flag, dew_point,
    relative_humidity, relative_humidity_flag, wind_speed, wind_speed_flag,
    wind_direction, station_pressure, visibility, precip_amount, bronze_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (climate_id, time_utc) DO UPDATE SET
    station_name           = EXCLUDED.station_name,
    province               = EXCLUDED.province,
    temp                   = EXCLUDED.temp,
    temp_flag              = EXCLUDED.temp_flag,
    dew_point              = EXCLUDED.dew_point,
    relative_humidity      = EXCLUDED.relative_humidity,
    relative_humidity_flag = EXCLUDED.relative_humidity_flag,
    wind_speed             = EXCLUDED.wind_speed,
    wind_speed_flag        = EXCLUDED.wind_speed_flag,
    wind_direction         = EXCLUDED.wind_direction,
    station_pressure       = EXCLUDED.station_pressure,
    visibility             = EXCLUDED.visibility,
    precip_amount          = EXCLUDED.precip_amount,
    bronze_key             = EXCLUDED.bronze_key,
    loaded_at              = now()`

func parseECCC(key string, data []byte) ([]statement, error) {
	obs, err := eccc.ParseHourly(data)
	if err != nil {
		return nil, err
	}
	stmts := make([]statement, 0, len(obs))
	for _, o := range obs {
		stmts = append(stmts, statement{upsertWeather, []any{
			o.ClimateID, o.TimeUTC, o.StationName, o.Province,
			o.Temp, nullable(o.TempFlag), o.DewPoint,
			o.RelativeHumidity, nullable(o.RelHumidityFlag),
			o.WindSpeed, nullable(o.WindSpeedFlag),
			o.WindDirection, o.StationPressure, o.Visibility, o.PrecipAmount, key,
		}})
	}
	return stmts, nil
}
