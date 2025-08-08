CREATE TABLE actions
(
    id         SERIAL PRIMARY KEY,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    user_id    INT         NOT NULL,
    action     VARCHAR(32) NOT NULL,
);