ROOT_DIR := ../..
# 不再有默认的 configs/dev.yaml（2026-09-29 删除：地址早已退役、凭据落盘）。
# 日常本机开发用仓库根的 scripts/dev-local.sh config（从 K8s 契约与 Secret 渲染 0600 临时配置）；
# 这里只在显式给 CONFIG_FILE 时直接 go run。
CONFIG_FILE ?=
SERVICE_NAME ?= config-service
SERVICE_VERSION ?= v1
DEPLOYMENT_MODE ?= dev
RUN_ARGS ?=

.PHONY: dev

# CONFIG_FILE 可指向本地自举配置；RUN_ARGS 透传 Go flag。
dev:
	@test -n "$(CONFIG_FILE)" || { echo "需要 CONFIG_FILE=<自举配置>；日常开发请用仓库根 scripts/dev-local.sh config"; exit 1; }
	cd $(ROOT_DIR) && CONFIG_FILE="$(CONFIG_FILE)" go run ./services/config/cmd/server \
		-serviceName="$(SERVICE_NAME)" \
		-serviceVersion="$(SERVICE_VERSION)" \
		-deploymentMode="$(DEPLOYMENT_MODE)" \
		$(RUN_ARGS)
