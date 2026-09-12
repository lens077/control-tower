CREATE SCHEMA IF NOT EXISTS config;
SET search_path TO config;

-- config.entry 配置项(键 + 当前值 + 元数据),键值粒度
CREATE TABLE IF NOT EXISTS config.entry (
    id           BIGSERIAL PRIMARY KEY,
    namespace    TEXT        NOT NULL,
    environment  TEXT        NOT NULL,
    key          TEXT        NOT NULL,
    format       TEXT        NOT NULL DEFAULT 'yaml',
    value        TEXT        NOT NULL DEFAULT '',
    version      INT         NOT NULL DEFAULT 1,
    is_secret    BOOLEAN     NOT NULL DEFAULT FALSE,
    description  TEXT        NOT NULL DEFAULT '',
    updated_by   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (namespace, environment, key)
);

-- config.revision 版本历史(append-only)
CREATE TABLE IF NOT EXISTS config.revision (
    id          BIGSERIAL   PRIMARY KEY,
    entry_id    BIGINT      NOT NULL REFERENCES config.entry (id) ON DELETE CASCADE,
    version     INT         NOT NULL,
    format      TEXT        NOT NULL,
    value       TEXT        NOT NULL,
    comment     TEXT        NOT NULL DEFAULT '',
    author      TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entry_id, version)
);

CREATE INDEX IF NOT EXISTS idx_entry_ns_env ON config.entry (namespace, environment);
CREATE INDEX IF NOT EXISTS idx_revision_entry ON config.revision (entry_id);

-- config.machine_token 数据面凭据(per-service × per-environment,设计见 docs/design/machine-token.md)
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
    last_used_at       TIMESTAMPTZ,
    -- 迁移 0003:'service'(数据面只读,默认)| 'operator'(管理面服务账号)
    role               TEXT        NOT NULL DEFAULT 'service'
);

CREATE INDEX IF NOT EXISTS idx_machine_token_svc_env ON config.machine_token (service_name, environment);
