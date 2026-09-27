.DEFAULT_GOAL := help

GO ?= go
PNPM ?= pnpm
ARGS ?=
LOCAL_TELEMETRY_ENDPOINT ?= https://telemetry.mewis.me/v1/products/codemcp/events
LOCAL_LDFLAGS = -X go.mewis.me/codemcp/internal/telemetry/product.Endpoint=$(LOCAL_TELEMETRY_ENDPOINT)

CM = $(GO) run -ldflags "$(LOCAL_LDFLAGS)" .
FRONTEND_BUILD = $(PNPM) --dir frontend build

CM_COMMANDS = install upgrade init uninit down logs request tui config auth instructions tools execution process workspace prompt upstream mcp tunnel server admin permissions shell notification telemetry telegram integration status doctor agent completion version
CM_FRONTEND_COMMANDS = up restart serve
CM_PASSTHROUGH_TARGETS = run $(CM_COMMANDS) $(CM_FRONTEND_COMMANDS)
CM_DEVELOPER_TARGETS = help bootstrap frontend-build check test test-race build frontend-dev
CM_KNOWN_TARGETS = $(CM_DEVELOPER_TARGETS) $(CM_PASSTHROUGH_TARGETS)
CM_PRIMARY_GOAL := $(firstword $(MAKECMDGOALS))
CM_FORWARDING := $(filter $(CM_PRIMARY_GOAL),$(CM_PASSTHROUGH_TARGETS))
CM_POSITIONAL_GOALS = $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
CM_FORWARD_EXTRA_GOALS = $(filter-out $(CM_KNOWN_TARGETS),$(CM_POSITIONAL_GOALS))
CM_QUOTE = '$(subst ','"'"',$(1))'
CM_POSITIONAL_ARGS = $(foreach arg,$(CM_POSITIONAL_GOALS),$(call CM_QUOTE,$(arg)))
CM_EFFECTIVE_ARGS = $(strip $(CM_POSITIONAL_ARGS) $(ARGS))

.PHONY: $(CM_DEVELOPER_TARGETS) run $(CM_COMMANDS) $(CM_FRONTEND_COMMANDS) $(CM_FORWARD_EXTRA_GOALS)

ifeq ($(CM_FORWARDING),)

help:
	@printf '%s\n' \
		'CodeMCP developer targets:' \
		'  bootstrap      Install deterministic frontend dependencies' \
		'  frontend-build Build embedded frontend assets' \
		'  check          Run fast local quality checks' \
		'  test           Run the full Go test suite with isolated config' \
		'  test-race      Run the full Go race suite with isolated config' \
		'  build          Build local dist/cm' \
		'  run            Build frontend, then run CodeMCP; pass ARGS="..."' \
		'  up|restart     Build frontend, then manage the runtime' \
		'  serve          Build frontend, then serve CodeMCP in foreground' \
		'  <cm-command>   Run any other public cm command' \
		'                  positional subcommands/args are forwarded directly' \
		'                  use ARGS="..." for flags or complex shell quoting' \
		'                  install upgrade init uninit down logs request tui config auth' \
		'                  instructions tools execution process workspace prompt upstream mcp tunnel' \
		'                  server admin permissions shell notification telemetry telegram integration' \
		'                  status doctor agent completion version' \
		'  frontend-dev   Run the Vite development server'

bootstrap:
	$(PNPM) --dir frontend install --frozen-lockfile

frontend-build:
	$(FRONTEND_BUILD)

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

build: frontend-build
	mkdir -p dist
	$(GO) build -trimpath -ldflags "$(LOCAL_LDFLAGS)" -o dist/cm .

frontend-dev:
	$(PNPM) --dir frontend dev

endif

run:
	$(if $(filter $@,$(CM_PRIMARY_GOAL)),$(FRONTEND_BUILD) && $(CM) $(CM_EFFECTIVE_ARGS),@:)

$(CM_FRONTEND_COMMANDS):
	$(if $(filter $@,$(CM_PRIMARY_GOAL)),$(FRONTEND_BUILD) && $(CM) $@ $(CM_EFFECTIVE_ARGS),@:)

$(CM_COMMANDS):
	$(if $(filter $@,$(CM_PRIMARY_GOAL)),$(CM) $@ $(CM_EFFECTIVE_ARGS),@:)

ifneq ($(CM_FORWARDING),)

$(CM_DEVELOPER_TARGETS):
	@:

$(CM_FORWARD_EXTRA_GOALS):
	@:

endif
