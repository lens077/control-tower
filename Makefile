# control-tower 聚合入口。目标保持幂等；CI 与本地共用。

.PHONY: api lint build test tidy verify

# 重新生成 proto 产物（Go + Connect）。
api:
	buf generate
	buf lint

lint:
	buf lint
	go vet ./...

build:
	go build ./...

test:
	go test -race -count=1 ./...

tidy:
	go mod tidy

# 提交前最小验证链。
verify: build lint test

# wire 冻结门禁：对旧 config-center 仓做 WIRE_JSON 口径的破坏性检查
# （go_package 更名与服务私有 conf.proto 搬家是预期差异，WIRE_JSON 不涉及）。
# 解除条件见 docs/design/decisions.md「config proto 大整形」。
# 跨版本实测：旧 SDK v0.1.0 → 新 config 服务（需要本机 docker；详见 scripts/crossversion.sh）。
test-crossversion:
	bash scripts/crossversion.sh

LEGACY_CONFIG_CENTER ?= ../config-center
breaking-legacy:
	buf breaking --against $(LEGACY_CONFIG_CENTER) \
		--config '{"version":"v2","modules":[{"path":".","excludes":["third_party/google","third_party/errors"]}],"breaking":{"use":["WIRE_JSON"]}}'
