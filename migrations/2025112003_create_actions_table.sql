-- +goose Up
CREATE TABLE actions
(
    id         UUID,
    created_at DateTime DEFAULT now(),
    user_id    UInt64,
    type       LowCardinality(String),
    network    LowCardinality(String),
    currency   LowCardinality(String),
    amount     Float64,
    hash       String,
    fee        Float64 DEFAULT 0,
    aml_id     String DEFAULT '',
    status     LowCardinality(String),

    INDEX      idx_status status TYPE set(0) GRANULARITY 1,
    INDEX      idx_type type TYPE set(0) GRANULARITY 1,
    INDEX      idx_network network TYPE set(0) GRANULARITY 1,
    INDEX      idx_currency currency TYPE set(0) GRANULARITY 1
) ENGINE = ReplacingMergeTree(created_at)
PARTITION BY toYYYYMM(created_at)
PRIMARY KEY (user_id, id)
ORDER BY (user_id, id)
SETTINGS index_granularity = 8192;

-- +goose Down
DROP TABLE actions;