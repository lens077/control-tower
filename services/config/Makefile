ROOT_DIR := ../..
CONFIG_FILE ?= $(CURDIR)/configs/dev.yaml
SERVICE_NAME ?= config-service
SERVICE_VERSION ?= v1
DEPLOYMENT_MODE ?= dev
RUN_ARGS ?=

.PHONY: dev

# CONFIG_FILE 可指向本地自举配置；RUN_ARGS 透传 Go flag。
dev:
	cd $(ROOT_DIR) && CONFIG_FILE="$(CONFIG_FILE)" go run ./services/config/cmd/server \
		-serviceName="$(SERVICE_NAME)" \
		-serviceVersion="$(SERVICE_VERSION)" \
		-deploymentMode="$(DEPLOYMENT_MODE)" \
		$(RUN_ARGS)
