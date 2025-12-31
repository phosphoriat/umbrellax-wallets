-- +goose Up
CREATE TABLE users
(
    created_at DateTime64(3) DEFAULT now64(3),
    user_id    UInt64,
    number     String,

    INDEX      idx_number number TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = ReplacingMergeTree(created_at)
PRIMARY KEY user_id
ORDER BY user_id
SETTINGS index_granularity = 8192;

-- +goose Down
DROP TABLE users;