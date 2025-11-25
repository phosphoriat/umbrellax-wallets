-- +goose Up
CREATE TABLE actions (
    created_at DateTime DEFAULT now(),
    user_id UInt32,
    action String,

    INDEX idx_user_id user_id TYPE minmax GRANULARITY 4
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(created_at)
PRIMARY KEY created_at
ORDER BY (created_at, user_id, action)
SETTINGS index_granularity = 8192;

-- +goose Down
DROP TABLE actions;