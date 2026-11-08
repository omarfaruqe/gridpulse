-- +goose Up
-- Statistics Canada table 25-10-0015-01: monthly generation in MWh by
-- province, class of producer and type of generation (Ontario, Quebec and
-- Alberta only). A blank value stays NULL, with StatCan's status symbol.
CREATE TABLE staging.statcan_generation_monthly (
    ref_month       date             NOT NULL, -- first day of the reference month
    geo             text             NOT NULL,
    producer_class  text             NOT NULL,
    generation_type text             NOT NULL,
    mwh             double precision,
    status          text,
    vector          text             NOT NULL,
    snapshot_utc    timestamptz      NOT NULL, -- download date; a newer download wins
    bronze_key      text             NOT NULL,
    loaded_at       timestamptz      NOT NULL DEFAULT now(),
    PRIMARY KEY (ref_month, geo, producer_class, generation_type)
);

-- +goose Down
DROP TABLE staging.statcan_generation_monthly;
