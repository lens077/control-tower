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
