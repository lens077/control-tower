-- +goose Up
-- machine token 角色：'service'（数据面只读，默认）| 'operator'（管理面服务账号）。
-- 设计：docs/design/machine-token.md「operator 角色」。
ALTER TABLE config.machine_token
    ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'service';

-- +goose Down
ALTER TABLE config.machine_token DROP COLUMN IF EXISTS role;
