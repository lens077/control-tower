ROOT_DIR := ../..
HTTP_ADDR ?= :8080
ADMIN_ADDR ?= :9090
CONFIG_SOURCE ?= file
CONFIG_DIR ?=
CONFIG_SOURCE_FILE ?=
JWT_ISSUER ?=
JWT_AUDIENCES ?=
RUN_ARGS ?=

.PHONY: dev check-config

# file 模式需传 CONFIG_DIR；config_center 模式需传 CONFIG_SOURCE_FILE。
dev: check-config
	cd $(ROOT_DIR) && \
		CONFIG_SOURCE="$(CONFIG_SOURCE)" \
		CONFIG_DIR="$(CONFIG_DIR)" \
		CONFIG_SOURCE_FILE="$(CONFIG_SOURCE_FILE)" \
		JWT_ISSUER="$(JWT_ISSUER)" \
		JWT_AUDIENCES="$(JWT_AUDIENCES)" \
		go run ./services/gateway/cmd/server \
		-httpAddr="$(HTTP_ADDR)" \
		-adminAddr="$(ADMIN_ADDR)" \
		$(RUN_ARGS)

check-config:
	@test -n "$(JWT_ISSUER)" || { echo "JWT_ISSUER 不能为空"; exit 1; }
	@test -n "$(JWT_AUDIENCES)" || { echo "JWT_AUDIENCES 不能为空"; exit 1; }
	@if [ "$(CONFIG_SOURCE)" = "file" ]; then \
		test -n "$(CONFIG_DIR)" || { echo "file 模式需要 CONFIG_DIR（包含五个配置工件）"; exit 1; }; \
	else \
		test -n "$(CONFIG_SOURCE_FILE)" || { echo "config_center 模式需要 CONFIG_SOURCE_FILE"; exit 1; }; \
	fi
