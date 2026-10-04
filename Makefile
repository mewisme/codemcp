.DEFAULT_GOAL := help

GO ?= go
PNPM ?= pnpm
NODE ?= node
GIT ?= git
ARGS ?=
BINARY ?= dist/cm
LOCAL_TELEMETRY_ENDPOINT ?=
LOCAL_LDFLAGS = $(if $(strip $(LOCAL_TELEMETRY_ENDPOINT)),-X go.mewis.me/codemcp/internal/telemetry/product.Endpoint=$(LOCAL_TELEMETRY_ENDPOINT),)
CM_CONFIG_ROOT ?= $(if $(strip $(CM_CONFIG_DIR)),$(CM_CONFIG_DIR),$(HOME)/.cm)
CM_GOCACHE ?= $(CM_CONFIG_ROOT)/runtime/cache/go-build
RACE_PACKAGES = ./internal/app ./internal/checkpoint ./internal/runtime/... ./internal/service ./internal/state ./internal/telegram/... ./internal/tools ./internal/workspace/...

CM = GOCACHE="$(CM_GOCACHE)" $(GO) run -ldflags "$(LOCAL_LDFLAGS)" .
FRONTEND_BUILD = $(PNPM) --dir frontend build

CM_COMMANDS = install upgrade init uninit down logs request llm tui config auth tools execution process workspace skills prompt upstream mcp tunnel http permissions shell notification telemetry telegram integration status health network activity doctor agent completion version
CM_FRONTEND_COMMANDS = up restart serve
CM_PASSTHROUGH_TARGETS = run $(CM_COMMANDS) $(CM_FRONTEND_COMMANDS)
CM_DEVELOPER_TARGETS = help bootstrap frontend-build check test test-race test-installer build frontend-dev generate check-generated install-local release-smoke security-gosec security-baseline
CM_KNOWN_TARGETS = $(CM_DEVELOPER_TARGETS) $(CM_PASSTHROUGH_TARGETS)
CM_PRIMARY_GOAL := $(firstword $(MAKECMDGOALS))
CM_FORWARDING := $(filter $(CM_PRIMARY_GOAL),$(CM_PASSTHROUGH_TARGETS))
CM_POSITIONAL_GOALS = $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
CM_QUOTE = '$(subst ','"'"',$(1))'
CM_POSITIONAL_ARGS = $(foreach arg,$(CM_POSITIONAL_GOALS),$(call CM_QUOTE,$(arg)))
CM_EFFECTIVE_ARGS = $(strip $(CM_POSITIONAL_ARGS) $(ARGS))

.PHONY: $(CM_DEVELOPER_TARGETS) run $(CM_COMMANDS) $(CM_FRONTEND_COMMANDS)

ifeq ($(CM_FORWARDING),)

help:
	@printf '%s\n' \
		'CodeMCP developer targets:' \
		'  bootstrap      Install deterministic frontend dependencies' \
		'  frontend-build Build embedded frontend assets' \
		'  check          Run fast local quality checks' \
		'  test           Run the full Go test suite with isolated config' \
		'  test-race      Run race-sensitive Go packages with isolated config' \
		'  test-installer Validate the Unix installer contract' \
		'  build          Build local dist/cm' \
		'  generate       Regenerate committed product presentation output' \
		'  check-generated Regenerate and fail if committed output drifts' \
		'  install-local  Build/install source locally; pass ARGS="..."' \
		'  release-smoke  Run built-binary smoke; override BINARY=path' \
		'  security-gosec Check the canonical gosec baseline' \
		'  security-baseline Regenerate the gosec baseline intentionally' \
		'  run            Build frontend, then run CodeMCP; pass ARGS="..."' \
		'  up|restart     Build frontend, then manage the runtime' \
		'  serve          Build frontend, then serve CodeMCP in foreground' \
		'  <cm-command>   Run any other public cm command' \
		'                  positional subcommands/args are forwarded directly' \
		'                  use ARGS="..." for flags or complex shell quoting' \
		'                  install upgrade init uninit down logs request llm tui config auth' \
		'                  tools execution process workspace skills prompt upstream mcp tunnel' \
		'                  http permissions shell notification telemetry telegram integration' \
		'                  status health network activity doctor agent completion version' \
		'  frontend-dev   Run the Vite development server'

bootstrap:
	$(PNPM) --dir frontend install --frozen-lockfile

frontend-build:
	$(FRONTEND_BUILD)

check: frontend-build
	./scripts/dev/check.sh

test: frontend-build
	@tmp=$$(mktemp -d) || exit 1; status=0; \
	CM_CONFIG_DIR="$$tmp" $(GO) test -count=1 ./... || status=$$?; \
	rm -rf "$$tmp"; exit $$status

test-race: frontend-build
	@tmp=$$(mktemp -d) || exit 1; status=0; \
	CM_CONFIG_DIR="$$tmp" $(GO) test -count=1 -race $(RACE_PACKAGES) || status=$$?; \
	rm -rf "$$tmp"; exit $$status

test-installer:
	sh scripts/installer/test-unix.sh

build: frontend-build
	mkdir -p dist
	$(GO) build -trimpath -ldflags "$(LOCAL_LDFLAGS)" -o dist/cm .

generate:
	$(GO) generate ./internal/productadapter

check-generated:
	$(GO) generate ./internal/productadapter
	$(GIT) diff --exit-code -- frontend/src/lib/operation-presentation.generated.ts

install-local:
	$(NODE) scripts/dev/install-local.mjs $(ARGS)

release-smoke:
	$(NODE) scripts/release/smoke.mjs $(BINARY)

security-gosec:
	$(NODE) scripts/ci/security/gosec-baseline.mjs

security-baseline:
	$(NODE) scripts/ci/security/gosec-baseline.mjs --update

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

%:
	@:

endif
