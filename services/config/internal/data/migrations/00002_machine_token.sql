-- +goose Up
-- per-service × per-environment 数据面凭据（设计：docs/design/machine-token.md）。
CREATE TABLE IF NOT EXISTS config.machine_token (
    id                 UUID        PRIMARY KEY,
    service_name       TEXT        NOT NULL,
    environment        TEXT        NOT NULL,
    token_hash         BYTEA       NOT NULL UNIQUE,
    allowed_namespaces TEXT[]      NOT NULL,
    note               TEXT        NOT NULL DEFAULT '',
    disabled           BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at         TIMESTAMPTZ,
    last_used_at       TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_machine_token_svc_env ON config.machine_token (service_name, environment);

-- +goose Down
DROP TABLE IF EXISTS config.machine_token;
