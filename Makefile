.DEFAULT_GOAL := help

GO ?= go
NODE ?= node
PNPM ?= pnpm
ARGS ?=
PREPARE_ARGS ?= --no-deps

CM = $(GO) run .
PREPARE_FRONTEND = $(NODE) scripts/prepare-frontend-embed.mjs

.PHONY: help bootstrap prepare check-embed check test test-race build run up restart down status logs tui frontend-dev

help:
	@printf '%s\n' \
		'CodeMCP developer targets:' \
		'  bootstrap      Install deterministic frontend dependencies' \
		'  prepare        Build and sync embedded frontend assets' \
		'  check-embed    Verify embedded frontend assets are in sync' \
		'  check          Run fast local quality checks' \
		'  test           Run the full Go test suite with isolated config' \
		'  test-race      Run the full Go race suite with isolated config' \
		'  build          Build local dist/cm' \
		'  run            Run CodeMCP; pass ARGS="..."' \
		'  up|restart     Prepare assets then manage the runtime' \
		'  down|status    Manage/query the runtime' \
		'  logs|tui       Open runtime logs or TUI' \
		'  frontend-dev   Run the Vite development server'

bootstrap:
	$(PNPM) --dir frontend install --frozen-lockfile

prepare:
	$(PREPARE_FRONTEND) $(PREPARE_ARGS)

check-embed:
	$(PREPARE_FRONTEND) --check

check:
	./scripts/check.sh

test:
	@tmp=$$(mktemp -d) || exit 1; status=0; \
	CM_CONFIG_DIR="$$tmp" $(GO) test -count=1 ./... || status=$$?; \
	rm -rf "$$tmp"; exit $$status

test-race:
	@tmp=$$(mktemp -d) || exit 1; status=0; \
	CM_CONFIG_DIR="$$tmp" $(GO) test -count=1 -race ./... || status=$$?; \
	rm -rf "$$tmp"; exit $$status

build: prepare
	mkdir -p dist
	$(GO) build -trimpath -o dist/cm .

run: prepare
	$(CM) $(ARGS)

up: prepare
	$(CM) up $(ARGS)

restart: prepare
	$(CM) restart $(ARGS)

down:
	$(CM) down $(ARGS)

status:
	$(CM) status $(ARGS)

logs:
	$(CM) logs $(ARGS)

tui:
	$(CM) tui $(ARGS)

frontend-dev:
	$(PNPM) --dir frontend dev
