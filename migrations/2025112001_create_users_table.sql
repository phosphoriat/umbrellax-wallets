-- +goose Up
CREATE TABLE users (
    created_at DateTime DEFAULT now(),
    user_id UInt32,
    number String
) ENGINE = ReplacingMergeTree(created_at)
PRIMARY KEY user_id
ORDER BY user_id
SETTINGS index_granularity = 8192;

-- +goose Down
DROP TABLE users;