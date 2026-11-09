# Data notes

What the data does and does not say, and the decisions behind the models. The findings come from checks against the live sources; where a cause is not established, the text says so.

## Time handling

Every source uses a different clock, so conversion to UTC is explicit and tested (`internal/timeutil`):

| Source | Clock | Handling |
|---|---|---|
| Hydro-Québec | Eastern local time, no offset | The repeated hour when clocks fall back is flagged `ambiguous`; the skipped hour when they spring forward is flagged `nonexistent` |
| IESO | Fixed EST (UTC-5) all year, hour-ending | Hour N starts at N-1 o'clock EST; no daylight saving shifts |
| AESO | UTC (`begin_datetime_utc`) | The local `begin_datetime_mpt` field is ignored: Alberta moved to year-round UTC-6 ("Alberta Time") on 2026-11-01, so history spans two time rule sets and UTC avoids the issue |
| ECCC weather | UTC (`UTC_DATE`) and local time | Requests use the `UTC_YEAR`/`UTC_MONTH`/`UTC_DAY` filters. The API's `datetime` filter matches the **local** clock, so a UTC window built with it silently shifts hours between days |

Empty values in source files are kept as nulls, never zero.

## Weather stations

Each grid region is represented by one or two ECCC hourly stations (`internal/extractors/eccc/stations.json`), with weights for combining them. A validator checks each region's weights sum to 1.

| Region (operator) | Stations | Weight |
|---|---|---|
| QC (Hydro-Québec) | Montréal Intl A, Québec Intl A | 0.84 / 0.16 |
| ON (IESO) | Toronto Intl A, Ottawa Intl A | 0.85 / 0.15 |
| AB (AESO) | Calgary Intl A, Edmonton Intl A | 0.51 / 0.49 |

The weights are approximate metro-population shares and should be refined with Statistics Canada census data. Station identifiers change when ECCC replaces equipment: the long-running Montréal airport ID `7025250` stopped reporting in 2013, and the current one is `7025251`. The archive trails real time by about a day, so each daily run should re-fetch recent days; re-fetching replaces the same bronze object.

## Freshness limits

A series is stale when its newest hour is older than its limit (`ops.freshness_sla`). The limits follow how each source publishes, not a wish for low latency.

The limits are first estimates. Every check records each series' lag in `ops.freshness_lag` (kept 90 days), and the pipeline page shows the worst lag seen in the last 7 days next to each limit, so a limit can be set from observed behaviour instead of a guess.

| Series | Limit | Why |
|---|---|---|
| load_QC | 3 h | Hydro-Québec feed updates every 15 minutes |
| load_AB, price_AB | 6 h, 12 h | AESO hourly API |
| load_ON | 36 h | IESO regenerates the yearly Demand file once a day, around 07:30 Eastern |
| price_ON | 12 h | Day-ahead prices are published ahead of time, so the lag can be negative |
| generation_ON | 30 h | IESO generator output reports |
| weather (all) | 48 h | The ECCC archive trails real time by about a day |

## Reconciliation with Statistics Canada

Ontario monthly generation from the IESO hourly report is compared with Statistics Canada table 25-10-0015-01 (MWh). Each comparison group is judged against its own trailing 12-month median ratio, because the two sources do not measure exactly the same thing and a ratio of 1 is not expected.

| Group | Tolerance around its own baseline |
|---|---|
| Nuclear | 0.02 |
| Total | 0.03 |
| Hydro | 0.06 |
| Wind | 0.08 |
| Solar | 0.10 |
| Combustible | 0.20 |

A month gets a status: `ok`, `out_of_band`, `incomplete` (IESO coverage of the six core fuels below 99.5%), `no_baseline` (fewer than 6 comparable months), `not_comparable` or `no_tolerance`. Months a person has examined go in `ops.recon_reviewed` and are skipped by the quality test.

What was observed (IESO ÷ StatCan):

- Nuclear is about 0.993, Total about 0.954, Hydro about 0.95 and Wind about 0.96.
- Solar is about 0.29. **The cause is likely a difference in scope but is not confirmed**; see below.
- Combustible varies between 0.78 and 0.97, which is why its tolerance is wide.
- Nuclear in 2023-07 and 2023-08 is outside its band. Both months are marked reviewed; **the cause has not been established.**
- Statistics Canada leaves Ontario's individual turbine categories blank since 2022, so the comparison uses Nuclear, Hydro, Wind, Solar, Combustible and Total only.

### Solar

The IESO hourly report covers generators of 20 MW or more registered with the IESO, and IESO states that distribution-connected (embedded) generation is not reported in its output reports. IESO also states that most Ontario solar is distribution-connected, and publishes transmission-connected solar output of 0.8 TWh for 2024 and 0.7 TWh for 2025.

| Year | IESO report (TWh) | StatCan (TWh) | Ratio |
|---|---|---|---|
| 2022 | 0.70 | 2.29 | 0.304 |
| 2023 | 0.68 | 2.30 | 0.296 |
| 2024 | 0.69 | 2.32 | 0.296 |
| 2025 | 0.67 | 2.28 | 0.294 |
| 2026 (8 months) | 0.48 | 1.63 | 0.292 |

What this supports: the pipeline's solar totals are close to IESO's own published figures (slightly lower, as expected from the 20 MW threshold), and the ratio is steady at 0.29 to 0.30 in every year, which fits a fixed share of solar that the report does not cover better than it fits a loading error. What is not established: how Statistics Canada defines solar generation in this table, so it is not confirmed that the remaining share is embedded solar. The reconciliation keeps comparing solar against its own baseline for that reason.

## Known gaps and limits

- Ontario 2025-05-01 hour 1 is missing upstream: the IESO file jumps from 2025-04-30 hour 24 to 2025-05-01 hour 2. The completeness test lists it as a known gap.
- Generation by fuel exists only for Ontario. Quebec and Alberta have no hourly generation series here.
- Quebec has no hourly market price, so there is no price series for QC.
- IESO publishes daily generation files for a short window only; the yearly `GenOutputbyFuelHourly` reports go back to 2022.
- `OutputQuality` is stored as published; its meaning is undocumented ("number of data points not available" is the closest description found).
- The fuel `CONTROL ACTIONS` appears in IESO data with zeros and nulls; it is kept in the fuel dimension and left out of the six core fuels used for reconciliation coverage.
- Missing values are stored as NULL and shown as gaps in the charts, never as zero.
- Weather is a weighted combination of one or two stations per region with approximate population weights; it is not a population-weighted regional temperature.

## Licences

Hydro-Québec open data is published under CC BY-NC 4.0, which does not allow commercial use. Check each provider's terms before reusing the data.
