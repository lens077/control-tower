PORT ?= 3005
MODE ?= dev
HOST ?= 0.0.0.0
DEV_ARGS ?=

.PHONY: install run

install:
	pnpm install

# DEV_ARGS 可继续透传给 vite-plus，例如 DEV_ARGS="--open"。
run:
	pnpm exec vp dev --host $(HOST) --port $(PORT) --mode $(MODE) $(DEV_ARGS)
