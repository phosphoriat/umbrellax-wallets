-- +goose Up
CREATE TABLE wallets (
    created_at DateTime DEFAULT now(),
    user_id UInt32,
    network LowCardinality(String),
    address String,

    INDEX idx_network network TYPE set(0) GRANULARITY 1,
    INDEX idx_address address TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = ReplacingMergeTree(created_at)
PRIMARY KEY (user_id, network)
ORDER BY (user_id, network)
SETTINGS index_granularity = 8192;

-- +goose Down
DROP TABLE wallets;