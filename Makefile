.DEFAULT_GOAL := help

GO ?= go
NODE ?= node
PNPM ?= pnpm
ARGS ?=
PREPARE_ARGS ?= --no-deps
LOCAL_TELEMETRY_ENDPOINT ?= https://telemetry.mewis.me/v1/products/codemcp/events
LOCAL_LDFLAGS = -X go.mewis.me/codemcp/internal/telemetry/product.Endpoint=$(LOCAL_TELEMETRY_ENDPOINT)

CM = $(GO) run -ldflags "$(LOCAL_LDFLAGS)" .
PREPARE_FRONTEND = $(NODE) scripts/prepare-frontend-embed.mjs

CM_COMMANDS = install upgrade init uninit down logs request tui config auth workspace prompt upstream mcp tunnel server admin permissions shell notification telemetry telegram integration status agent completion version
CM_PREPARE_COMMANDS = up restart serve

.PHONY: help bootstrap prepare check-embed check test test-race build run frontend-dev $(CM_COMMANDS) $(CM_PREPARE_COMMANDS)

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
		'  run            Prepare assets, then run CodeMCP; pass ARGS="..."' \
		'  up|restart     Prepare assets, then manage the runtime' \
		'  serve          Prepare assets, then serve CodeMCP in foreground' \
		'  <cm-command>   Run any other public cm command; pass ARGS="..."' \
		'                  install upgrade init uninit down logs request tui' \
		'                  config auth workspace prompt upstream mcp tunnel' \
		'                  server admin permissions shell notification telemetry' \
		'                  telegram integration status agent completion version' \
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
	$(GO) build -trimpath -ldflags "$(LOCAL_LDFLAGS)" -o dist/cm .

run: prepare
	$(CM) $(ARGS)

$(CM_PREPARE_COMMANDS): prepare
	$(CM) $@ $(ARGS)

$(CM_COMMANDS):
	$(CM) $@ $(ARGS)

frontend-dev:
	$(PNPM) --dir frontend dev
