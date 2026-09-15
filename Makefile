# control-tower 聚合入口。目标保持幂等；CI 与本地共用。

.PHONY: api api-go api-ts tools check-gen lint build test tidy verify sync-ecommerce-schemas

# 重新生成 proto 产物：Go + Connect（buf.gen.yaml）与控制台 TS（buf.gen.ts.yaml → web/src/gen）。
# 两份必须一起出：只跑 Go 那份会让 web/src/gen 悄悄落后于 proto（2026-09-15 实测 system.proto
# 的 build_version 字段就是这样漏掉的），所以 api 不再只等于 buf generate。
api: api-go api-ts
	buf lint

api-go:
	buf generate

# 把 Go 插件钉到 go.mod 里的库版本：生成物头部带插件版本号，插件漂移就是一次假 diff。
tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$$(go list -m -f '{{.Version}}' google.golang.org/protobuf)
	go install connectrpc.com/connect/cmd/protoc-gen-connect-go@$$(go list -m -f '{{.Version}}' connectrpc.com/connect)

# protoc-gen-es 来自 web/node_modules（先 cd web && pnpm install）。
# NODE_OPTIONS 关掉 Node 25+ 的实验性 Web Storage，否则每个插件进程都刷一条无关警告。
api-ts:
	PATH="$(CURDIR)/web/node_modules/.bin:$$PATH" NODE_OPTIONS=--no-experimental-webstorage \
		buf generate --template buf.gen.ts.yaml

# 生成物门禁：proto 改了但没重新生成（或生成物被误删）时在这里变红，而不是在控制台运行时。
check-gen: api
	@git diff --exit-code --stat -- '*.pb.go' '*.connect.go' web/src/gen \
		|| { echo "generated files are out of date: run 'make api' and commit the result"; exit 1; }
	@untracked=$$(git ls-files --others --exclude-standard -- web/src/gen '*.pb.go' '*.connect.go'); \
		if [ -n "$$untracked" ]; then echo "untracked generated files:"; echo "$$untracked"; exit 1; fi

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

# 从 sibling ecommerce 仓同步 10 个服务的 Bootstrap Schema 快照。
sync-ecommerce-schemas:
	bash scripts/sync-ecommerce-schemas.sh

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
