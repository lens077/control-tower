-- +goose Up
-- 注意：迁移内禁止 SET search_path —— goose 的版本表在 public，
-- 改会话 search_path 会让同连接后续的版本表写入解析失败（实测踩过）。
-- 本文件所有对象均显式 schema 限定。
CREATE SCHEMA IF NOT EXISTS config;

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

-- +goose Down
-- baseline 不提供回滚（存量库快照）。
