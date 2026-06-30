# Makefile for Bifrost

# Variables
HOST ?= localhost
PORT ?= 8080
APP_DIR ?=
PROMETHEUS_LABELS ?=
LOG_STYLE ?= json
LOG_LEVEL ?= info
TEST_REPORTS_DIR ?= test-reports
GOTESTSUM_FORMAT ?= standard-verbose
FLOW ?=
VERSION ?= dev-build
LOCAL ?=
DEBUG ?=
COMPAT ?=

# Colors for output
RED=\033[0;31m
GREEN=\033[0;32m
YELLOW=\033[1;33m
BLUE=\033[0;34m
CYAN=\033[0;36m
NC=\033[0m # No Color
ECHO := printf '%b\n'

# nvm requires bash-compatible shell semantics; /bin/sh is dash on some Linux distros.
SHELL := /usr/bin/env bash

# Ensures the Node version pinned in .nvmrc is active before any npm/node call.
# nvm is a shell function, so each recipe that needs it must inline this snippet
# via `$(USE_NODE); <your command>`.
USE_NODE = NVM_SH="$${NVM_DIR:-$$HOME/.nvm}/nvm.sh"; \
	[ -s "$$NVM_SH" ] || NVM_SH="$$(brew --prefix nvm 2>/dev/null)/nvm.sh"; \
	if [ -s "$$NVM_SH" ]; then . "$$NVM_SH" >/dev/null && nvm install >/dev/null 2>&1 && nvm use >/dev/null 2>&1; fi

# Loads secrets into the current recipe shell. Reads USE_INFISICAL env var:
#   USE_INFISICAL=1  -> source secrets from Infisical (`infisical export --path <p>`)
#   anything else    -> source ./.env (legacy default)
# Honors INFISICAL_PATH (default /local) when USE_INFISICAL=1.
# After invoking `$(EXPOSE_ENV);`, all subsequent commands inherit the secrets
# - no per-command prefix needed.
# Use as: `$(EXPOSE_ENV); <your command>`
define EXPOSE_ENV
	case "$$USE_INFISICAL" in \
		1|y|Y|yes|YES|true|TRUE) USE_INFISICAL_RESOLVED=1 ;; \
		*) USE_INFISICAL_RESOLVED=0 ;; \
	esac; \
	if [ "$$USE_INFISICAL_RESOLVED" = "1" ]; then \
		if ! which infisical > /dev/null 2>&1; then \
			$(ECHO) "$(RED)infisical CLI not found. Install: https://infisical.com/docs/cli/overview$(NC)"; \
			exit 1; \
		fi; \
		INFISICAL_PATH_VAL="$${INFISICAL_PATH:-/local}"; \
		$(ECHO) "$(GREEN)Sourcing secrets from Infisical (path=$$INFISICAL_PATH_VAL)$(NC)"; \
		if ! infisical export --path "$$INFISICAL_PATH_VAL" --format dotenv > /dev/null 2>&1; then \
			$(ECHO) "$(RED)Failed to export secrets from Infisical (path=$$INFISICAL_PATH_VAL)$(NC)"; \
			infisical export --path "$$INFISICAL_PATH_VAL" --format dotenv 2>&1 >/dev/null; \
			exit 1; \
		fi; \
		set -a; . <(infisical export --path "$$INFISICAL_PATH_VAL" --format dotenv); set +a; \
	else \
		if [ -f .env ]; then \
			$(ECHO) "$(YELLOW)Loading environment variables from .env...$(NC)"; \
			set -a; . ./.env; set +a; \
		fi; \
	fi
endef

.PHONY: all help dev dev-pulse build-ui build run install-air install-pulse clean test install-ui setup-workspace work-init work-clean docker-image docker-run cleanup-enterprise mod-tidy format ui

all: help

# Default target
help: ## Show this help message
	@$(ECHO) "$(BLUE)Bifrost Development - Available Commands:$(NC)"
	@$(ECHO) ""
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  $(GREEN)%-15s$(NC) %s\n", $$1, $$2}'
	@$(ECHO) ""
	@$(ECHO) "$(YELLOW)Environment Variables:$(NC)"
	@$(ECHO) "  HOST              Server host (default: localhost)"
	@$(ECHO) "  PORT              Server port (default: 8080)"
	@$(ECHO) "  PROMETHEUS_LABELS Labels for Prometheus metrics"
	@$(ECHO) "  LOG_STYLE         Logger output format: json|pretty (default: json)"
	@$(ECHO) "  LOG_LEVEL         Logger level: debug|info|warn|error (default: info)"
	@$(ECHO) "  APP_DIR           App data directory inside container (default: /app/data)"
	@$(ECHO) "  LOCAL             Use local go.work for builds (e.g., make build LOCAL=1)"
	@$(ECHO) "  DEBUG             Enable delve debugger on port 2345 (e.g., make dev DEBUG=1, make test-core DEBUG=1, make test-governance DEBUG=1)"
	@$(ECHO) ""
	@$(ECHO) "$(YELLOW)Test Configuration:$(NC)"
	@$(ECHO) "  TEST_REPORTS_DIR  Directory for HTML test reports (default: test-reports)"
	@$(ECHO) "  GOTESTSUM_FORMAT  Test output format: testname|dots|pkgname|standard-verbose (default: standard-verbose)"
	@$(ECHO) "  TESTCASE          Exact test name to run (e.g., TestVirtualKeyTokenRateLimit)"
	@$(ECHO) "  PATTERN           Substring pattern to filter tests (alternative to TESTCASE)"
	@$(ECHO) "  FLOW              E2E test flow to run: providers|virtual-keys (default: all)"

cleanup-enterprise: ## Clean up enterprise directories if present
	@$(ECHO) "$(GREEN)Cleaning up enterprise...$(NC)"
	@if [ -d "ui/app/enterprise" ]; then rm -rf ui/app/enterprise; fi
	@$(ECHO) "$(GREEN)Enterprise cleaned up$(NC)"

install-ui: cleanup-enterprise
	@$(USE_NODE); \
	 which node > /dev/null || ($(ECHO) "$(RED)Error: Node.js is not installed. Please install Node.js first.$(NC)" && exit 1); \
	 which npm > /dev/null || ($(ECHO) "$(RED)Error: npm is not installed. Please install npm first.$(NC)" && exit 1); \
	 $(ECHO) "$(GREEN)Node.js $$(node -v) and npm $$(npm -v) are installed$(NC)"; \
	 if [ ! -d "ui/node_modules" ] || [ "ui/package.json" -nt "ui/node_modules/.package-lock.json" ] || [ "ui/package-lock.json" -nt "ui/node_modules/.package-lock.json" ]; then \
	   $(ECHO) "$(YELLOW)Dependencies changed, running npm ci...$(NC)"; \
	   cd ui && npm ci; \
	 else \
	   $(ECHO) "$(GREEN)UI dependencies up to date, skipping install$(NC)"; \
	 fi
	@$(ECHO) "$(GREEN)UI deps are in sync$(NC)"

install-air: ## Install air for hot reloading (if not already installed)
	@which air > /dev/null || ($(ECHO) "$(YELLOW)Installing air for hot reloading...$(NC)" && go install github.com/air-verse/air@latest)
	@$(ECHO) "$(GREEN)Air is ready$(NC)"

install-pulse: ## Install pulse for hot reloading (if not already installed)
	@which pulse > /dev/null || ($(ECHO) "$(YELLOW)Installing pulse for hot reloading...$(NC)" && go install github.com/Pratham-Mishra04/pulse@latest)
	@$(ECHO) "$(GREEN)Pulse is ready$(NC)"

install-delve: ## Install delve for debugging (if not already installed)
	@which dlv > /dev/null || ($(ECHO) "$(YELLOW)Installing delve for debugging...$(NC)" && go install github.com/go-delve/delve/cmd/dlv@latest)
	@$(ECHO) "$(GREEN)Delve is ready$(NC)"

install-gotestsum: ## Install gotestsum for test reporting (if not already installed)
	@which gotestsum > /dev/null || ($(ECHO) "$(YELLOW)Installing gotestsum for test reporting...$(NC)" && go install gotest.tools/gotestsum@latest)
	@$(ECHO) "$(GREEN)gotestsum is ready$(NC)"

install-junit-viewer: ## Install junit-viewer for HTML report generation (if not already installed)
	@if [ -z "$$CI" ] && [ -z "$$GITHUB_ACTIONS" ] && [ -z "$$GITLAB_CI" ] && [ -z "$$CIRCLECI" ] && [ -z "$$JENKINS_HOME" ]; then \
		if which junit-viewer > /dev/null 2>&1; then \
			$(ECHO) "$(GREEN)junit-viewer is already installed$(NC)"; \
		else \
			$(ECHO) "$(YELLOW)Installing junit-viewer for HTML reports...$(NC)"; \
			$(USE_NODE); \
			if npm install -g junit-viewer 2>&1; then \
				$(ECHO) "$(GREEN)junit-viewer installed successfully$(NC)"; \
			else \
				$(ECHO) "$(RED)Failed to install junit-viewer. HTML reports will be skipped.$(NC)"; \
				$(ECHO) "$(YELLOW)You can install it manually: npm install -g junit-viewer$(NC)"; \
				exit 0; \
			fi; \
		fi; \
	else \
		$(ECHO) "$(YELLOW)CI environment detected, skipping junit-viewer installation$(NC)"; \
	fi

dev: install-ui install-air setup-workspace $(if $(DEBUG),install-delve) ## Start complete development environment (UI + API with proxy)
	@$(EXPOSE_ENV); \
	set +m; \
	ui_pid=""; \
	api_pid=""; \
	cleanup() { \
		$(ECHO) "$(YELLOW)[make dev] cleanup started; ui_pid=$$ui_pid api_pid=$$api_pid$(NC)"; \
		trap - EXIT INT TERM HUP; \
		for pid in "$$ui_pid" "$$api_pid"; do \
			if [ -n "$$pid" ]; then \
				children="$$(pgrep -P "$$pid" 2>/dev/null || true)"; \
				$(ECHO) "$(YELLOW)[make dev] sending TERM to pid $$pid and children: $${children:-none}$(NC)"; \
				kill -TERM $$children "$$pid" 2>/dev/null || true; \
			fi; \
		done; \
		sleep 1; \
		for pid in "$$ui_pid" "$$api_pid"; do \
			if [ -n "$$pid" ]; then \
				children="$$(pgrep -P "$$pid" 2>/dev/null || true)"; \
				$(ECHO) "$(YELLOW)[make dev] sending KILL to pid $$pid and remaining children: $${children:-none}$(NC)"; \
				kill -KILL $$children "$$pid" 2>/dev/null || true; \
			fi; \
		done; \
		$(ECHO) "$(YELLOW)[make dev] waiting for background jobs to exit...$(NC)"; \
		wait 2>/dev/null || true; \
		$(ECHO) "$(GREEN)[make dev] cleanup completed.$(NC)"; \
	}; \
	stop_dev() { \
		$(ECHO) "$(YELLOW)[make dev] received shutdown signal; starting cleanup...$(NC)"; \
		cleanup; \
		exit 130; \
	}; \
	trap cleanup EXIT; \
	trap stop_dev INT TERM HUP; \
	$(ECHO) "$(GREEN)Starting Bifrost complete development environment...$(NC)"; \
	$(ECHO) "$(YELLOW)This will start:$(NC)"; \
	$(ECHO) "  1. UI development server (localhost:3000)"; \
	$(ECHO) "  2. API server with UI proxy (localhost:$(PORT))"; \
	$(ECHO) "$(CYAN)Access everything at: http://localhost:$(PORT)$(NC)"; \
	if [ -n "$(DEBUG)" ]; then \
		$(ECHO) "$(CYAN)  3. Debugger (delve) listening on port 2345$(NC)"; \
	fi; \
	if [ ! -d "transports/bifrost-http/ui" ]; then \
		$(ECHO) "$(YELLOW)Creating transports/bifrost-http/ui directory...$(NC)"; \
		mkdir -p transports/bifrost-http/ui; \
		touch transports/bifrost-http/ui/.tmp; \
	fi; \
	$(ECHO) ""; \
	$(ECHO) "$(YELLOW)Starting UI development server...$(NC)"; \
	$(USE_NODE); (cd ui && npm run dev) & \
	ui_pid="$$!"; \
	$(ECHO) "$(YELLOW)[make dev] UI dev server started with pid $$ui_pid$(NC)"; \
	sleep 3; \
	$(ECHO) "$(YELLOW)Starting API server with UI proxy...$(NC)"; \
	$(MAKE) setup-workspace >/dev/null; \
	if [ -n "$(DEBUG)" ]; then \
		$(ECHO) "$(CYAN)Starting with air + delve debugger on port 2345...$(NC)"; \
		$(ECHO) "$(YELLOW)Attach your debugger to localhost:2345$(NC)"; \
		(cd transports/bifrost-http && BIFROST_UI_DEV=true air -c .air.debug.toml -- \
			-host "$(HOST)" \
			-port "$(PORT)" \
			-log-style "$(LOG_STYLE)" \
			-log-level "$(LOG_LEVEL)" \
			$(if $(PROMETHEUS_LABELS),-prometheus-labels "$(PROMETHEUS_LABELS)") \
			$(if $(APP_DIR),-app-dir "$(abspath $(APP_DIR))")) & \
	else \
		(cd transports/bifrost-http && BIFROST_UI_DEV=true air -c .air.toml -- \
			-host "$(HOST)" \
			-port "$(PORT)" \
			-log-style "$(LOG_STYLE)" \
			-log-level "$(LOG_LEVEL)" \
			$(if $(PROMETHEUS_LABELS),-prometheus-labels "$(PROMETHEUS_LABELS)") \
			$(if $(APP_DIR),-app-dir "$(abspath $(APP_DIR))")) & \
	fi; \
	api_pid="$$!"; \
	$(ECHO) "$(YELLOW)[make dev] API dev server started with pid $$api_pid$(NC)"; \
	while kill -0 "$$ui_pid" 2>/dev/null && kill -0 "$$api_pid" 2>/dev/null; do sleep 1; done; \
	$(ECHO) "$(YELLOW)[make dev] one of the dev processes exited; running cleanup...$(NC)"; \
	cleanup; \
	exit 1

dev-pulse: install-ui install-pulse setup-workspace $(if $(DEBUG),install-delve) ## Start complete development environment using pulse for hot reloading
	@$(EXPOSE_ENV); \
	set +m; \
	ui_pid=""; \
	pulse_pid=""; \
	cleanup() { \
		$(ECHO) "$(YELLOW)[make dev-pulse] cleanup started; ui_pid=$$ui_pid pulse_pid=$$pulse_pid$(NC)"; \
		trap - EXIT INT TERM HUP; \
		for pid in "$$ui_pid" "$$pulse_pid"; do \
			if [ -n "$$pid" ]; then \
				children="$$(pgrep -P "$$pid" 2>/dev/null || true)"; \
				$(ECHO) "$(YELLOW)[make dev-pulse] sending TERM to pid $$pid and children: $${children:-none}$(NC)"; \
				kill -TERM $$children "$$pid" 2>/dev/null || true; \
			fi; \
		done; \
		sleep 1; \
		for pid in "$$ui_pid" "$$pulse_pid"; do \
			if [ -n "$$pid" ]; then \
				children="$$(pgrep -P "$$pid" 2>/dev/null || true)"; \
				$(ECHO) "$(YELLOW)[make dev-pulse] sending KILL to pid $$pid and remaining children: $${children:-none}$(NC)"; \
				kill -KILL $$children "$$pid" 2>/dev/null || true; \
			fi; \
		done; \
		$(ECHO) "$(YELLOW)[make dev-pulse] waiting for background jobs to exit...$(NC)"; \
		wait 2>/dev/null || true; \
		$(ECHO) "$(GREEN)[make dev-pulse] cleanup completed.$(NC)"; \
	}; \
	stop_dev() { \
		$(ECHO) "$(YELLOW)[make dev-pulse] received shutdown signal; starting cleanup...$(NC)"; \
		cleanup; \
		exit 130; \
	}; \
	trap cleanup EXIT; \
	trap stop_dev INT TERM HUP; \
	$(ECHO) "$(GREEN)Starting Bifrost complete development environment (pulse)...$(NC)"; \
	$(ECHO) "$(YELLOW)This will start:$(NC)"; \
	$(ECHO) "  1. UI development server (localhost:3000)"; \
	$(ECHO) "  2. API server with UI proxy (localhost:$(PORT))"; \
	$(ECHO) "$(CYAN)Access everything at: http://localhost:$(PORT)$(NC)"; \
	if [ -n "$(DEBUG)" ]; then \
		$(ECHO) "$(CYAN)  3. Debugger (delve) listening on port 2345$(NC)"; \
	fi; \
	if [ ! -d "transports/bifrost-http/ui" ]; then \
		$(ECHO) "$(YELLOW)Creating transports/bifrost-http/ui directory...$(NC)"; \
		mkdir -p transports/bifrost-http/ui; \
		touch transports/bifrost-http/ui/.tmp; \
	fi; \
	$(ECHO) ""; \
	$(ECHO) "$(YELLOW)Starting UI development server...$(NC)"; \
	$(USE_NODE); (cd ui && npm run dev) & \
	ui_pid="$$!"; \
	$(ECHO) "$(YELLOW)[make dev-pulse] UI dev server started with pid $$ui_pid$(NC)"; \
	sleep 3; \
	$(ECHO) "$(YELLOW)Starting API server with UI proxy...$(NC)"; \
	$(MAKE) setup-workspace >/dev/null; \
	if [ -n "$(DEBUG)" ]; then \
		$(ECHO) "$(CYAN)Starting with pulse + delve debugger on port 2345...$(NC)"; \
		$(ECHO) "$(YELLOW)Attach your debugger to localhost:2345$(NC)"; \
		PORT="$(PORT)" HOST="$(HOST)" LOG_STYLE="$(LOG_STYLE)" LOG_LEVEL="$(LOG_LEVEL)" BIFROST_UI_DEV=true \
			$(if $(APP_DIR),APP_DIR="$(abspath $(APP_DIR))") pulse & \
	else \
		PORT="$(PORT)" HOST="$(HOST)" LOG_STYLE="$(LOG_STYLE)" LOG_LEVEL="$(LOG_LEVEL)" BIFROST_UI_DEV=true \
			$(if $(APP_DIR),APP_DIR="$(abspath $(APP_DIR))") pulse & \
	fi; \
	pulse_pid="$$!"; \
	$(ECHO) "$(YELLOW)[make dev-pulse] pulse started with pid $$pulse_pid$(NC)"; \
	while kill -0 "$$ui_pid" 2>/dev/null && kill -0 "$$pulse_pid" 2>/dev/null; do sleep 1; done; \
	$(ECHO) "$(YELLOW)[make dev-pulse] one of the dev processes exited; running cleanup...$(NC)"; \
	cleanup; \
	exit 1

build-ui: install-ui ## Build ui
	@$(ECHO) "$(GREEN)Building ui...$(NC)"
	@rm -rf ui/.next
	@$(USE_NODE); cd ui && npm run build && npm run copy-build

build: build-ui ## Build bifrost-http binary
	@if [ -n "$(LOCAL)" ]; then \
		$(ECHO) "$(GREEN)╔═══════════════════════════════════════════════╗$(NC)"; \
		$(ECHO) "$(GREEN)║  Building bifrost-http with local go.work...  ║$(NC)"; \
		$(ECHO) "$(GREEN)╚═══════════════════════════════════════════════╝$(NC)"; \
	else \
		$(ECHO) "$(GREEN)╔═══════════════════════════════════════╗$(NC)"; \
		$(ECHO) "$(GREEN)║  Building bifrost-http...             ║$(NC)"; \
		$(ECHO) "$(GREEN)╚═══════════════════════════════════════╝$(NC)"; \
	fi
	@if [ -n "$(DYNAMIC)" ]; then \
		$(ECHO) "$(YELLOW)Note: This will create a dynamically linked build.$(NC)"; \
	else \
		$(ECHO) "$(YELLOW)Note: This will create a statically linked build.$(NC)"; \
	fi
	@mkdir -p ./tmp
	@TARGET_OS="$(GOOS)"; \
	TARGET_ARCH="$(GOARCH)"; \
	ACTUAL_OS=$$(uname -s | tr '[:upper:]' '[:lower:]' | sed 's/darwin/darwin/;s/linux/linux/;s/mingw.*/windows/'); \
	ACTUAL_ARCH=$$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/;s/arm64/arm64/'); \
	if [ -z "$$TARGET_OS" ]; then \
		TARGET_OS=$$ACTUAL_OS; \
	fi; \
	if [ -z "$$TARGET_ARCH" ]; then \
		TARGET_ARCH=$$ACTUAL_ARCH; \
	fi; \
	HOST_OS=$$ACTUAL_OS; \
	HOST_ARCH=$$ACTUAL_ARCH; \
	$(ECHO) "$(CYAN)Host: $$HOST_OS/$$HOST_ARCH | Target: $$TARGET_OS/$$TARGET_ARCH$(NC)"; \
	if [ "$$TARGET_OS" = "linux" ] && [ "$$HOST_OS" = "linux" ]; then \
		if [ -n "$(DYNAMIC)" ]; then \
			$(ECHO) "$(CYAN)Building for $$TARGET_OS/$$TARGET_ARCH with dynamic linking...$(NC)"; \
			cd transports/bifrost-http && CGO_ENABLED=1 GOOS=$$TARGET_OS GOARCH=$$TARGET_ARCH $(if $(LOCAL),,GOWORK=off) go build \
				-ldflags="-w -s -X main.Version=v$(VERSION)" \
				-a -trimpath \
				-o ../../tmp/bifrost-http \
				.; \
		else \
			$(ECHO) "$(CYAN)Building for $$TARGET_OS/$$TARGET_ARCH with static linking...$(NC)"; \
			cd transports/bifrost-http && CGO_ENABLED=1 GOOS=$$TARGET_OS GOARCH=$$TARGET_ARCH $(if $(LOCAL),,GOWORK=off) go build \
				-ldflags="-w -s -extldflags "-static" -X main.Version=v$(VERSION)" \
				-a -trimpath \
				-tags "sqlite_static" \
				-o ../../tmp/bifrost-http \
				.; \
		fi; \
		$(ECHO) "$(GREEN)Built: tmp/bifrost-http (version: v$(VERSION))$(NC)"; \
	elif [ "$$TARGET_OS" = "$$HOST_OS" ] && [ "$$TARGET_ARCH" = "$$HOST_ARCH" ]; then \
		$(ECHO) "$(CYAN)Building for $$TARGET_OS/$$TARGET_ARCH (native build with CGO)...$(NC)"; \
		cd transports/bifrost-http && CGO_ENABLED=1 GOOS=$$TARGET_OS GOARCH=$$TARGET_ARCH $(if $(LOCAL),,GOWORK=off) go build \
			-ldflags="-w -s -X main.Version=v$(VERSION)" \
			-a -trimpath \
			-tags "sqlite_static" \
			-o ../../tmp/bifrost-http \
			.; \
		$(ECHO) "$(GREEN)Built: tmp/bifrost-http (version: v$(VERSION))$(NC)"; \
	else \
		$(ECHO) "$(YELLOW)Cross-compilation detected: $$HOST_OS/$$HOST_ARCH -> $$TARGET_OS/$$TARGET_ARCH$(NC)"; \
		$(ECHO) "$(CYAN)Using Docker for cross-compilation...$(NC)"; \
		$(MAKE) _build-with-docker TARGET_OS=$$TARGET_OS TARGET_ARCH=$$TARGET_ARCH $(if $(DYNAMIC),DYNAMIC=$(DYNAMIC)); \
	fi

_build-with-docker: # Internal target for Docker-based cross-compilation
	@$(ECHO) "$(CYAN)Using Docker for cross-compilation...$(NC)"; \
	if [ "$(TARGET_OS)" = "linux" ]; then \
		if [ -n "$(DYNAMIC)" ]; then \
			$(ECHO) "$(CYAN)Building for $(TARGET_OS)/$(TARGET_ARCH) in Docker container with dynamic linking...$(NC)"; \
			docker run --rm \
				--platform linux/$(TARGET_ARCH) \
				-v "$(shell pwd):/workspace" \
				-w /workspace/transports/bifrost-http \
				-e CGO_ENABLED=1 \
				-e GOOS=$(TARGET_OS) \
				-e GOARCH=$(TARGET_ARCH) \
				 $(if $(LOCAL),,-e GOWORK=off) \
				golang:1.26.4-alpine3.23@sha256:f23e8b227fb4493eabe03bede4d5a32d04092da71962f1fb79b5f7d1e6c2a17f \
				sh -c "apk add --no-cache gcc musl-dev && \
				go build \
					-ldflags='-w -s -X main.Version=v$(VERSION)' \
					-a -trimpath \
					-o ../../tmp/bifrost-http \
					."; \
		else \
			$(ECHO) "$(CYAN)Building for $(TARGET_OS)/$(TARGET_ARCH) in Docker container...$(NC)"; \
			docker run --rm \
				--platform linux/$(TARGET_ARCH) \
				-v "$(shell pwd):/workspace" \
				-w /workspace/transports/bifrost-http \
				-e CGO_ENABLED=1 \
				-e GOOS=$(TARGET_OS) \
				-e GOARCH=$(TARGET_ARCH) \
				 $(if $(LOCAL),,-e GOWORK=off) \
				golang:1.26.4-alpine3.23@sha256:f23e8b227fb4493eabe03bede4d5a32d04092da71962f1fb79b5f7d1e6c2a17f \
				sh -c "apk add --no-cache gcc musl-dev && \
				go build \
					-ldflags='-w -s -extldflags "-static" -X main.Version=v$(VERSION)' \
					-a -trimpath \
					-tags sqlite_static \
					-o ../../tmp/bifrost-http \
					."; \
		fi; \
		$(ECHO) "$(GREEN)Built: tmp/bifrost-http ($(TARGET_OS)/$(TARGET_ARCH), version: v$(VERSION))$(NC)"; \
	else \
		$(ECHO) "$(RED)Error: Docker cross-compilation only supports Linux targets$(NC)"; \
		$(ECHO) "$(YELLOW)For $(TARGET_OS), please build on a native $(TARGET_OS) machine$(NC)"; \
		exit 1; \
	fi

docker-image: build-ui ## Build Docker image (LOCAL=1 to use Dockerfile.local)
	@$(ECHO) "$(GREEN)Building Docker image...$(NC)"
	$(eval GIT_SHA=$(shell git rev-parse --short HEAD))
	$(eval DOCKERFILE=$(if $(LOCAL),transports/Dockerfile.local,transports/Dockerfile))
	@docker build -f $(DOCKERFILE) -t bifrost -t bifrost:$(GIT_SHA) -t bifrost:latest .
	@$(ECHO) "$(GREEN)Docker image built: bifrost, bifrost:$(GIT_SHA), bifrost:latest (using $(DOCKERFILE))$(NC)"

docker-run: ## Run Docker container (Usage: make docker-run [CONFIG=path/to/config.json or path/to/dir/])
	@$(ECHO) "$(GREEN)Running Docker container...$(NC)"
	@CONFIG_PATH="$(abspath $(CONFIG))"; \
	if [ -n "$(CONFIG)" ]; then \
		if [ -d "$$CONFIG_PATH" ]; then \
			CONFIG_PATH="$$CONFIG_PATH/config.json"; \
		fi; \
		CONFIG_MOUNT="-v $$CONFIG_PATH:/app/data/config.json"; \
	else \
		CONFIG_MOUNT=""; \
	fi; \
	docker run -e APP_PORT=$(PORT) -e APP_HOST=0.0.0.0 -p $(PORT):$(PORT) -e LOG_LEVEL=$(LOG_LEVEL) -e LOG_STYLE=$(LOG_STYLE) -v $(shell pwd):/app/data $$CONFIG_MOUNT bifrost

run: build ## Build and run bifrost-http (no hot reload)
	@$(ECHO) "$(GREEN)Running bifrost-http...$(NC)"
	@./tmp/bifrost-http \
		-host "$(HOST)" \
		-port "$(PORT)" \
		-log-style "$(LOG_STYLE)" \
		-log-level "$(LOG_LEVEL)" \
		$(if $(PROMETHEUS_LABELS),-prometheus-labels "$(PROMETHEUS_LABELS)") \
		$(if $(APP_DIR),-app-dir "$(abspath $(APP_DIR))")

clean: ## Clean build artifacts and temporary files
	@$(ECHO) "$(YELLOW)Cleaning build artifacts...$(NC)"
	@rm -rf tmp/
	@rm -f transports/bifrost-http/build-errors.log
	@rm -rf transports/bifrost-http/tmp/
	@rm -rf $(TEST_REPORTS_DIR)/
	@$(ECHO) "$(GREEN)Clean complete$(NC)"

clean-test-reports: ## Clean test reports only
	@$(ECHO) "$(YELLOW)Cleaning test reports...$(NC)"
	@rm -rf $(TEST_REPORTS_DIR)/
	@$(ECHO) "$(GREEN)Test reports cleaned$(NC)"

generate-html-reports: ## Convert existing XML reports to HTML
	@if ! which junit-viewer > /dev/null 2>&1; then \
		$(ECHO) "$(RED)Error: junit-viewer not installed$(NC)"; \
		$(ECHO) "$(YELLOW)Install with: make install-junit-viewer$(NC)"; \
		exit 1; \
	fi
	@$(ECHO) "$(GREEN)Converting XML reports to HTML...$(NC)"
	@if [ ! -d "$(TEST_REPORTS_DIR)" ] || [ -z "$$(ls -A $(TEST_REPORTS_DIR)/*.xml 2>/dev/null)" ]; then \
		$(ECHO) "$(YELLOW)No XML reports found in $(TEST_REPORTS_DIR)$(NC)"; \
		$(ECHO) "$(YELLOW)Run tests first: make test-all$(NC)"; \
		exit 0; \
	fi
	@for xml in $(TEST_REPORTS_DIR)/*.xml; do \
		html=$${xml%.xml}.html; \
		$(ECHO) "  Converting $$(basename $$xml) → $$(basename $$html)"; \
		junit-viewer --results=$$xml --save=$$html 2>/dev/null || true; \
	done
	@$(ECHO) ""
	@$(ECHO) "$(GREEN)✓ HTML reports generated$(NC)"
	@$(ECHO) "$(CYAN)View reports:$(NC)"
	@ls -1 $(TEST_REPORTS_DIR)/*.html 2>/dev/null | sed 's|$(TEST_REPORTS_DIR)/|  open $(TEST_REPORTS_DIR)/|' || true

test: install-gotestsum ## Run tests for bifrost-http
	@$(ECHO) "$(GREEN)Running bifrost-http tests...$(NC)"
	@mkdir -p $(TEST_REPORTS_DIR)
	@cd transports/bifrost-http && GOWORK=off gotestsum \
		--format=$(GOTESTSUM_FORMAT) \
		--junitfile=../../$(TEST_REPORTS_DIR)/bifrost-http.xml \
		-- -v ./...
	@if [ -z "$$CI" ] && [ -z "$$GITHUB_ACTIONS" ] && [ -z "$$GITLAB_CI" ] && [ -z "$$CIRCLECI" ] && [ -z "$$JENKINS_HOME" ]; then \
		if which junit-viewer > /dev/null 2>&1; then \
			$(ECHO) "$(YELLOW)Generating HTML report...$(NC)"; \
			if junit-viewer --results=$(TEST_REPORTS_DIR)/bifrost-http.xml --save=$(TEST_REPORTS_DIR)/bifrost-http.html 2>/dev/null; then \
				$(ECHO) ""; \
				$(ECHO) "$(CYAN)HTML report: $(TEST_REPORTS_DIR)/bifrost-http.html$(NC)"; \
				$(ECHO) "$(CYAN)Open with: open $(TEST_REPORTS_DIR)/bifrost-http.html$(NC)"; \
			else \
				$(ECHO) "$(YELLOW)HTML generation failed. JUnit XML report available.$(NC)"; \
				$(ECHO) "$(CYAN)JUnit XML report: $(TEST_REPORTS_DIR)/bifrost-http.xml$(NC)"; \
			fi; \
		else \
			$(ECHO) ""; \
			$(ECHO) "$(YELLOW)junit-viewer not installed. Install with: make install-junit-viewer$(NC)"; \
			$(ECHO) "$(CYAN)JUnit XML report: $(TEST_REPORTS_DIR)/bifrost-http.xml$(NC)"; \
		fi; \
	else \
		$(ECHO) ""; \
		$(ECHO) "$(CYAN)JUnit XML report: $(TEST_REPORTS_DIR)/bifrost-http.xml$(NC)"; \
	fi

test-core: install-gotestsum $(if $(DEBUG),install-delve) ## Run core tests (Usage: make test-core PROVIDER=openai TESTCASE=TestName or PATTERN=substring, DEBUG=1 for debugger)
	@$(EXPOSE_ENV); \
	$(ECHO) "$(GREEN)Running core tests...$(NC)"; \
	mkdir -p $(TEST_REPORTS_DIR); \
	if [ -n "$(PATTERN)" ] && [ -n "$(TESTCASE)" ]; then \
		$(ECHO) "$(RED)Error: PATTERN and TESTCASE are mutually exclusive$(NC)"; \
		$(ECHO) "$(YELLOW)Use PATTERN for substring matching or TESTCASE for exact match$(NC)"; \
		exit 1; \
	fi; \
	TEST_FAILED=0; \
	REPORT_FILE=""; \
	if [ -n "$(PROVIDER)" ]; then \
		$(ECHO) "$(CYAN)Running tests for provider: $(PROVIDER)$(NC)"; \
		if [ ! -f "core/providers/$(PROVIDER)/$(PROVIDER)_test.go" ]; then \
			$(ECHO) "$(RED)Error: Provider test file '$(PROVIDER)_test.go' not found in core/providers/$(PROVIDER)/$(NC)"; \
			$(ECHO) "$(YELLOW)Available providers:$(NC)"; \
			find core/providers -name "*_test.go" -type f 2>/dev/null | sed 's|core/providers/\([^/]*\)/.*|\1|' | sort -u | sed 's/^/  - /'; \
			exit 1; \
		fi; \
	fi; \
	if [ -n "$(DEBUG)" ]; then \
		$(ECHO) "$(CYAN)Debug mode enabled - delve debugger will listen on port 2345$(NC)"; \
		$(ECHO) "$(YELLOW)Attach your debugger to localhost:2345$(NC)"; \
	fi; \
	if [ -n "$(PROVIDER)" ]; then \
		PROVIDER_TEST_NAME=$$($(ECHO) "$(PROVIDER)" | awk '{print toupper(substr($$0,1,1)) tolower(substr($$0,2))}' | sed 's/openai/OpenAI/i; s/openrouter/OpenRouter/i; s/sgl/SGL/i; s/xai/XAI/i'); \
		if [ -n "$(TESTCASE)" ]; then \
			CLEAN_TESTCASE="$(TESTCASE)"; \
			CLEAN_TESTCASE=$${CLEAN_TESTCASE#Test$${PROVIDER_TEST_NAME}/}; \
			CLEAN_TESTCASE=$${CLEAN_TESTCASE#$${PROVIDER_TEST_NAME}Tests/}; \
			CLEAN_TESTCASE=$$($(ECHO) "$$CLEAN_TESTCASE" | sed 's|^Test[A-Z][A-Za-z]*/[A-Z][A-Za-z]*Tests/||'); \
			$(ECHO) "$(CYAN)Running Test$${PROVIDER_TEST_NAME}/$${PROVIDER_TEST_NAME}Tests/$$CLEAN_TESTCASE...$(NC)"; \
			REPORT_FILE="$(TEST_REPORTS_DIR)/core-$(PROVIDER)-$$(echo $$CLEAN_TESTCASE | sed 's|/|_|g').xml"; \
			if [ -n "$(DEBUG)" ]; then \
				cd core/providers/$(PROVIDER) && GOWORK=off dlv test --headless --listen=:2345 --api-version=2 -- -test.v -test.run "^Test$${PROVIDER_TEST_NAME}$$/.*Tests/$$CLEAN_TESTCASE$$" || TEST_FAILED=1; \
			else \
				cd core/providers/$(PROVIDER) && GOWORK=off gotestsum \
					--format=$(GOTESTSUM_FORMAT) \
					--junitfile=../../../$$REPORT_FILE \
					-- -v -timeout 20m -run "^Test$${PROVIDER_TEST_NAME}$$/.*Tests/$$CLEAN_TESTCASE$$" || TEST_FAILED=1; \
			fi; \
			cd ../../..; \
			$(MAKE) cleanup-junit-xml REPORT_FILE=$$REPORT_FILE; \
			if [ -z "$$CI" ] && [ -z "$$GITHUB_ACTIONS" ] && [ -z "$$GITLAB_CI" ] && [ -z "$$CIRCLECI" ] && [ -z "$$JENKINS_HOME" ]; then \
				if which junit-viewer > /dev/null 2>&1; then \
					$(ECHO) "$(YELLOW)Generating HTML report...$(NC)"; \
					junit-viewer --results=$$REPORT_FILE --save=$${REPORT_FILE%.xml}.html 2>/dev/null || true; \
					$(ECHO) ""; \
					$(ECHO) "$(CYAN)HTML report: $${REPORT_FILE%.xml}.html$(NC)"; \
					$(ECHO) "$(CYAN)Open with: open $${REPORT_FILE%.xml}.html$(NC)"; \
				else \
					$(ECHO) ""; \
					$(ECHO) "$(CYAN)JUnit XML report: $$REPORT_FILE$(NC)"; \
				fi; \
			else \
				$(ECHO) ""; \
				$(ECHO) "$(CYAN)JUnit XML report: $$REPORT_FILE$(NC)"; \
			fi; \
		elif [ -n "$(PATTERN)" ]; then \
			$(ECHO) "$(CYAN)Running tests matching '$(PATTERN)' for $${PROVIDER_TEST_NAME}...$(NC)"; \
			REPORT_FILE="$(TEST_REPORTS_DIR)/core-$(PROVIDER)-$(PATTERN).xml"; \
			if [ -n "$(DEBUG)" ]; then \
				cd core/providers/$(PROVIDER) && GOWORK=off dlv test --headless --listen=:2345 --api-version=2 -- -test.v -test.run ".*$(PATTERN).*" || TEST_FAILED=1; \
			else \
				cd core/providers/$(PROVIDER) && GOWORK=off gotestsum \
					--format=$(GOTESTSUM_FORMAT) \
					--junitfile=../../../$$REPORT_FILE \
					-- -v -timeout 20m -run ".*$(PATTERN).*" || TEST_FAILED=1; \
			fi; \
			cd ../../..; \
			$(MAKE) cleanup-junit-xml REPORT_FILE=$$REPORT_FILE; \
			if [ -z "$$CI" ] && [ -z "$$GITHUB_ACTIONS" ] && [ -z "$$GITLAB_CI" ] && [ -z "$$CIRCLECI" ] && [ -z "$$JENKINS_HOME" ]; then \
				if which junit-viewer > /dev/null 2>&1; then \
					$(ECHO) "$(YELLOW)Generating HTML report...$(NC)"; \
					junit-viewer --results=$$REPORT_FILE --save=$${REPORT_FILE%.xml}.html 2>/dev/null || true; \
					$(ECHO) ""; \
					$(ECHO) "$(CYAN)HTML report: $${REPORT_FILE%.xml}.html$(NC)"; \
					$(ECHO) "$(CYAN)Open with: open $${REPORT_FILE%.xml}.html$(NC)"; \
				else \
					$(ECHO) ""; \
					$(ECHO) "$(CYAN)JUnit XML report: $$REPORT_FILE$(NC)"; \
				fi; \
			else \
				$(ECHO) ""; \
				$(ECHO) "$(CYAN)JUnit XML report: $$REPORT_FILE$(NC)"; \
			fi; \
		else \
			$(ECHO) "$(CYAN)Running Test$${PROVIDER_TEST_NAME}...$(NC)"; \
			REPORT_FILE="$(TEST_REPORTS_DIR)/core-$(PROVIDER).xml"; \
			if [ -n "$(DEBUG)" ]; then \
				cd core/providers/$(PROVIDER) && GOWORK=off dlv test --headless --listen=:2345 --api-version=2 -- -test.v -test.run "^Test$${PROVIDER_TEST_NAME}$$" || TEST_FAILED=1; \
			else \
				cd core/providers/$(PROVIDER) && GOWORK=off gotestsum \
					--format=$(GOTESTSUM_FORMAT) \
					--junitfile=../../../$$REPORT_FILE \
					-- -v -timeout 20m -run "^Test$${PROVIDER_TEST_NAME}$$" || TEST_FAILED=1; \
			fi; \
			cd ../../..; \
			$(MAKE) cleanup-junit-xml REPORT_FILE=$$REPORT_FILE; \
			if [ -z "$$CI" ] && [ -z "$$GITHUB_ACTIONS" ] && [ -z "$$GITLAB_CI" ] && [ -z "$$CIRCLECI" ] && [ -z "$$JENKINS_HOME" ]; then \
				if which junit-viewer > /dev/null 2>&1; then \
					$(ECHO) "$(YELLOW)Generating HTML report...$(NC)"; \
					junit-viewer --results=$$REPORT_FILE --save=$${REPORT_FILE%.xml}.html 2>/dev/null || true; \
					$(ECHO) ""; \
					$(ECHO) "$(CYAN)HTML report: $${REPORT_FILE%.xml}.html$(NC)"; \
					$(ECHO) "$(CYAN)Open with: open $${REPORT_FILE%.xml}.html$(NC)"; \
				else \
					$(ECHO) ""; \
					$(ECHO) "$(CYAN)JUnit XML report: $$REPORT_FILE$(NC)"; \
				fi; \
			else \
				$(ECHO) ""; \
				$(ECHO) "$(CYAN)JUnit XML report: $$REPORT_FILE$(NC)"; \
			fi; \
		fi; \
	else \
		if [ -n "$(TESTCASE)" ]; then \
			$(ECHO) "$(RED)Error: TESTCASE requires PROVIDER to be specified$(NC)"; \
			$(ECHO) "$(YELLOW)Usage: make test-core PROVIDER=openai TESTCASE=SpeechSynthesisStreamAdvanced/MultipleVoices_Streaming/StreamingVoice_echo$(NC)"; \
			exit 1; \
		fi; \
		if [ -n "$(PATTERN)" ]; then \
			$(ECHO) "$(CYAN)Running tests matching '$(PATTERN)' across all providers...$(NC)"; \
			REPORT_FILE="$(TEST_REPORTS_DIR)/core-all-$(PATTERN).xml"; \
			if [ -n "$(DEBUG)" ]; then \
				cd core && GOWORK=off dlv test --headless --listen=:2345 --api-version=2 ./providers/... -- -test.v -test.run ".*$(PATTERN).*" || TEST_FAILED=1; \
			else \
				cd core && GOWORK=off gotestsum \
					--format=$(GOTESTSUM_FORMAT) \
					--junitfile=../$$REPORT_FILE \
					-- -v -timeout 20m -run ".*$(PATTERN).*" ./providers/... || TEST_FAILED=1; \
			fi; \
		else \
			REPORT_FILE="$(TEST_REPORTS_DIR)/core-all.xml"; \
			if [ -n "$(DEBUG)" ]; then \
				cd core && GOWORK=off dlv test --headless --listen=:2345 --api-version=2 ./providers/... -- -test.v || TEST_FAILED=1; \
			else \
				cd core && GOWORK=off gotestsum \
					--format=$(GOTESTSUM_FORMAT) \
					--junitfile=../$$REPORT_FILE \
					-- -v ./providers/... || TEST_FAILED=1; \
			fi; \
		fi; \
		cd ..; \
		$(MAKE) cleanup-junit-xml REPORT_FILE=$$REPORT_FILE; \
		if [ -z "$$CI" ] && [ -z "$$GITHUB_ACTIONS" ] && [ -z "$$GITLAB_CI" ] && [ -z "$$CIRCLECI" ] && [ -z "$$JENKINS_HOME" ]; then \
			if which junit-viewer > /dev/null 2>&1; then \
				$(ECHO) "$(YELLOW)Generating HTML report...$(NC)"; \
				junit-viewer --results=$$REPORT_FILE --save=$${REPORT_FILE%.xml}.html 2>/dev/null || true; \
				$(ECHO) ""; \
				$(ECHO) "$(CYAN)HTML report: $${REPORT_FILE%.xml}.html$(NC)"; \
				$(ECHO) "$(CYAN)Open with: open $${REPORT_FILE%.xml}.html$(NC)"; \
			else \
				$(ECHO) ""; \
				$(ECHO) "$(CYAN)JUnit XML report: $$REPORT_FILE$(NC)"; \
			fi; \
		else \
			$(ECHO) ""; \
			$(ECHO) "$(CYAN)JUnit XML report: $$REPORT_FILE$(NC)"; \
		fi; \
	fi; \
	if [ -f "$$REPORT_FILE" ]; then \
		ALL_FAILED=$$(grep -B 1 '<failure' "$$REPORT_FILE" 2>/dev/null | \
			grep '<testcase' | \
			sed 's/.*name="\([^"]*\)".*/\1/' | \
			sort -u); \
		MAX_DEPTH=$$($(ECHO) "$$ALL_FAILED" | awk -F'/' '{print NF}' | sort -n | tail -1); \
		FAILED_TESTS=$$($(ECHO) "$$ALL_FAILED" | awk -F'/' -v max="$$MAX_DEPTH" 'NF == max'); \
		FAILURES=$$($(ECHO) "$$FAILED_TESTS" | grep -v '^$$' | wc -l | tr -d ' '); \
		if [ "$$FAILURES" -gt 0 ]; then \
			$(ECHO) ""; \
			$(ECHO) "$(RED)═══════════════════════════════════════════════════════════$(NC)"; \
			$(ECHO) "$(RED)                    FAILED TEST CASES                      $(NC)"; \
			$(ECHO) "$(RED)═══════════════════════════════════════════════════════════$(NC)"; \
			$(ECHO) ""; \
			printf "$(YELLOW)%-60s %-20s$(NC)\n" "Test Name" "Status"; \
			printf "$(YELLOW)%-60s %-20s$(NC)\n" "─────────────────────────────────────────────────────────────" "────────────────────"; \
			$(ECHO) "$$FAILED_TESTS" | while read -r testname; do \
				if [ -n "$$testname" ]; then \
					printf "$(RED)%-60s %-20s$(NC)\n" "$$testname" "FAILED"; \
				fi; \
			done; \
			$(ECHO) ""; \
			$(ECHO) "$(RED)Total Failures: $$FAILURES$(NC)"; \
			$(ECHO) ""; \
		else \
			$(ECHO) ""; \
			$(ECHO) "$(GREEN)═══════════════════════════════════════════════════════════$(NC)"; \
			$(ECHO) "$(GREEN)                 ALL TESTS PASSED ✓                       $(NC)"; \
			$(ECHO) "$(GREEN)═══════════════════════════════════════════════════════════$(NC)"; \
			$(ECHO) ""; \
		fi; \
	fi; \
	if [ -n "$$REPORT_FILE" ] && [ -f "$$REPORT_FILE" ]; then \
		SUMMARY_PREFIX=$$(basename "$$REPORT_FILE" .xml | sed 's/-.*//'); \
		SUMMARY_TITLE=$$($(ECHO) "$$SUMMARY_PREFIX" | awk '{print toupper(substr($$0,1,1)) substr($$0,2)}'); \
		$(MAKE) --no-print-directory print-test-summary \
			SUMMARY_LABEL="$$SUMMARY_TITLE" \
			SUMMARY_STRIP="$$SUMMARY_PREFIX-" \
			SUMMARY_FILES="$$REPORT_FILE"; \
	fi; \
	if [ $$TEST_FAILED -eq 1 ]; then \
		exit 1; \
	fi

cleanup-junit-xml: ## Internal: Clean up JUnit XML to remove parent test cases with child failures
	@if [ -z "$(REPORT_FILE)" ]; then \
		$(ECHO) "$(RED)Error: REPORT_FILE not specified$(NC)"; \
		exit 1; \
	fi
	@if [ ! -f "$(REPORT_FILE)" ]; then \
		exit 0; \
	fi
	@ALL_FAILED=$$(grep -B 1 '<failure' "$(REPORT_FILE)" 2>/dev/null | \
		grep '<testcase' | \
		sed 's/.*name="\([^"]*\)".*/\1/' | \
		sort -u); \
	if [ -n "$$ALL_FAILED" ]; then \
		MAX_DEPTH=$$($(ECHO) "$$ALL_FAILED" | awk -F'/' '{print NF}' | sort -n | tail -1); \
		PARENT_TESTS=$$($(ECHO) "$$ALL_FAILED" | awk -F'/' -v max="$$MAX_DEPTH" 'NF < max'); \
		if [ -n "$$PARENT_TESTS" ]; then \
			cp "$(REPORT_FILE)" "$(REPORT_FILE).tmp"; \
			$(ECHO) "$$PARENT_TESTS" | while IFS= read -r parent; do \
				if [ -n "$$parent" ]; then \
					ESCAPED=$$($(ECHO) "$$parent" | sed 's/[\/&]/\\&/g'); \
					perl -i -pe 'BEGIN{undef $$/;} s/<testcase[^>]*name="'"$$ESCAPED"'"[^>]*>.*?<failure.*?<\/testcase>//gs' "$(REPORT_FILE).tmp" 2>/dev/null || true; \
				fi; \
			done; \
			if [ -f "$(REPORT_FILE).tmp" ]; then \
				mv "$(REPORT_FILE).tmp" "$(REPORT_FILE)"; \
			fi; \
		fi; \
	fi

test-plugins: install-gotestsum ## Run plugin tests
	@$(EXPOSE_ENV); \
	$(ECHO) "$(GREEN)Running plugin tests...$(NC)"; \
	mkdir -p $(TEST_REPORTS_DIR); \
	cd plugins && find . -name "*.go" -path "*/tests/*" -o -name "*_test.go" | head -1 > /dev/null && \
		for dir in $$(find . -name "*_test.go" -exec dirname {} \; | sort -u); do \
			plugin_name=$$(echo $$dir | sed 's|^\./||' | sed 's|/|-|g'); \
			$(ECHO) "Testing $$dir..."; \
			cd $$dir && gotestsum \
				--format=$(GOTESTSUM_FORMAT) \
				--junitfile=../../$(TEST_REPORTS_DIR)/plugin-$$plugin_name.xml \
				-- -v ./... && cd - > /dev/null; \
			if [ -z "$$CI" ] && [ -z "$$GITHUB_ACTIONS" ] && [ -z "$$GITLAB_CI" ] && [ -z "$$CIRCLECI" ] && [ -z "$$JENKINS_HOME" ]; then \
				if which junit-viewer > /dev/null 2>&1; then \
					$(ECHO) "$(YELLOW)Generating HTML report for $$plugin_name...$(NC)"; \
					junit-viewer --results=../$(TEST_REPORTS_DIR)/plugin-$$plugin_name.xml --save=../$(TEST_REPORTS_DIR)/plugin-$$plugin_name.html 2>/dev/null || true; \
				fi; \
			fi; \
		done || $(ECHO) "No plugin tests found"
	@$(ECHO) ""
	@if [ -z "$$CI" ] && [ -z "$$GITHUB_ACTIONS" ] && [ -z "$$GITLAB_CI" ] && [ -z "$$CIRCLECI" ] && [ -z "$$JENKINS_HOME" ]; then \
		$(ECHO) "$(CYAN)HTML reports saved to $(TEST_REPORTS_DIR)/plugin-*.html$(NC)"; \
	else \
		$(ECHO) "$(CYAN)JUnit XML reports saved to $(TEST_REPORTS_DIR)/plugin-*.xml$(NC)"; \
	fi
	@$(MAKE) --no-print-directory print-test-summary \
		SUMMARY_LABEL="Plugin" \
		SUMMARY_STRIP="plugin-" \
		SUMMARY_FILES="$(TEST_REPORTS_DIR)/plugin-*.xml"

test-framework: install-gotestsum ## Run framework tests
	@$(EXPOSE_ENV); \
	$(ECHO) "$(GREEN)Running framework tests...$(NC)"; \
	mkdir -p $(TEST_REPORTS_DIR); \
	cd framework && find . -name "*.go" -path "*/tests/*" -o -name "*_test.go" | head -1 > /dev/null && \
		for dir in $$(find . -name "*_test.go" -exec dirname {} \; | sort -u); do \
			pkg_name=$$(echo $$dir | sed 's|^\./||' | sed 's|/|-|g'); \
			$(ECHO) "Testing $$dir..."; \
			cd $$dir && gotestsum \
				--format=$(GOTESTSUM_FORMAT) \
				--junitfile=../../$(TEST_REPORTS_DIR)/framework-$$pkg_name.xml \
				-- -v ./... && cd - > /dev/null; \
			if [ -z "$$CI" ] && [ -z "$$GITHUB_ACTIONS" ] && [ -z "$$GITLAB_CI" ] && [ -z "$$CIRCLECI" ] && [ -z "$$JENKINS_HOME" ]; then \
				if which junit-viewer > /dev/null 2>&1; then \
					$(ECHO) "$(YELLOW)Generating HTML report for $$pkg_name...$(NC)"; \
					junit-viewer --results=../$(TEST_REPORTS_DIR)/framework-$$pkg_name.xml --save=../$(TEST_REPORTS_DIR)/framework-$$pkg_name.html 2>/dev/null || true; \
				fi; \
			fi; \
		done || $(ECHO) "No framework tests found"
	@$(ECHO) ""
	@if [ -z "$$CI" ] && [ -z "$$GITHUB_ACTIONS" ] && [ -z "$$GITLAB_CI" ] && [ -z "$$CIRCLECI" ] && [ -z "$$JENKINS_HOME" ]; then \
		$(ECHO) "$(CYAN)HTML reports saved to $(TEST_REPORTS_DIR)/framework-*.html$(NC)"; \
	else \
		$(ECHO) "$(CYAN)JUnit XML reports saved to $(TEST_REPORTS_DIR)/framework-*.xml$(NC)"; \
	fi
	@$(MAKE) --no-print-directory print-test-summary \
		SUMMARY_LABEL="Framework" \
		SUMMARY_STRIP="framework-" \
		SUMMARY_FILES="$(TEST_REPORTS_DIR)/framework-*.xml"

# Internal: render a table of test reports + a final pass/fail scenario.
# Usage: $(MAKE) print-test-summary SUMMARY_LABEL="Framework" SUMMARY_STRIP="framework-" SUMMARY_FILES="<glob or files>"
# Each report becomes one row: tests/failures/errors come from the <testsuites> aggregate,
# while skipped is summed from the per-<testsuite> attrs (the aggregate omits skipped).
SUMMARY_SEP := --------------------------------------------------
print-test-summary:
	@$(ECHO) ""; \
	$(ECHO) "$(CYAN)============================================================================$(NC)"; \
	$(ECHO) "$(CYAN)$(SUMMARY_LABEL) Test Report Summary$(NC)"; \
	$(ECHO) "$(CYAN)============================================================================$(NC)"; \
	total_tests=0; total_pass=0; total_fail=0; total_err=0; total_skip=0; reports=0; \
	printf "%-50s %7s %7s %7s %7s %7s\n" "REPORT" "TESTS" "PASS" "FAIL" "ERR" "SKIP"; \
	printf "%-50s %7s %7s %7s %7s %7s\n" "$(SUMMARY_SEP)" "-------" "-------" "-------" "-------" "-------"; \
	for xml in $(SUMMARY_FILES); do \
		[ -e "$$xml" ] || continue; \
		line=$$(grep -o '<testsuites[^>]*>' "$$xml" | head -1); \
		t=$$(printf '%s' "$$line" | sed -n 's/.*[^a-z]tests="\([0-9]*\)".*/\1/p'); \
		f=$$(printf '%s' "$$line" | sed -n 's/.*failures="\([0-9]*\)".*/\1/p'); \
		e=$$(printf '%s' "$$line" | sed -n 's/.*errors="\([0-9]*\)".*/\1/p'); \
		s=$$(grep -o '<testsuite [^>]*>' "$$xml" | grep -o 'skipped="[0-9]*"' | grep -o '[0-9]*' | awk '{x+=$$1} END{print x+0}'); \
		t=$${t:-0}; f=$${f:-0}; e=$${e:-0}; s=$${s:-0}; \
		p=$$((t - f - e - s)); \
		name=$$(basename "$$xml" .xml | sed 's/^$(SUMMARY_STRIP)//'); \
		reports=$$((reports + 1)); \
		total_tests=$$((total_tests + t)); total_pass=$$((total_pass + p)); \
		total_fail=$$((total_fail + f)); total_err=$$((total_err + e)); total_skip=$$((total_skip + s)); \
		if [ $$((f + e)) -gt 0 ]; then color="$(RED)"; else color="$(GREEN)"; fi; \
		printf "%b%-50s %7s %7s %7s %7s %7s%b\n" "$$color" "$$name" "$$t" "$$p" "$$f" "$$e" "$$s" "$(NC)"; \
	done; \
	printf "%-50s %7s %7s %7s %7s %7s\n" "$(SUMMARY_SEP)" "-------" "-------" "-------" "-------" "-------"; \
	printf "%-50s %7s %7s %7s %7s %7s\n" "TOTAL ($$reports reports)" "$$total_tests" "$$total_pass" "$$total_fail" "$$total_err" "$$total_skip"; \
	$(ECHO) "$(CYAN)============================================================================$(NC)"; \
	if [ "$$reports" -eq 0 ]; then \
		$(ECHO) "$(YELLOW)No $(SUMMARY_LABEL) test reports found$(NC)"; \
	elif [ $$((total_fail + total_err)) -eq 0 ]; then \
		$(ECHO) "$(GREEN)✓ ALL $(SUMMARY_LABEL) TESTS PASSED ($$total_pass/$$total_tests passed, $$total_skip skipped)$(NC)"; \
	else \
		$(ECHO) "$(RED)✗ $(SUMMARY_LABEL) TESTS FAILED ($$total_fail failures, $$total_err errors out of $$total_tests tests)$(NC)"; \
	fi

test-http-transport: install-gotestsum ## Run HTTP transport tests
	@$(EXPOSE_ENV); \
	$(ECHO) "$(GREEN)Running HTTP transport tests...$(NC)"; \
	mkdir -p $(TEST_REPORTS_DIR); \
	cd transports/bifrost-http && find . -name "*.go" -path "*/tests/*" -o -name "*_test.go" | head -1 > /dev/null && \
		for dir in $$(find . -name "*_test.go" -exec dirname {} \; | sort -u); do \
			pkg_name=$$(echo $$dir | sed 's|^\./||' | sed 's|/|-|g'); \
			$(ECHO) "Testing $$dir..."; \
			cd $$dir && gotestsum \
				--format=$(GOTESTSUM_FORMAT) \
				--junitfile=../../../$(TEST_REPORTS_DIR)/http-transport-$$pkg_name.xml \
				-- -v ./... && cd - > /dev/null; \
			if [ -z "$$CI" ] && [ -z "$$GITHUB_ACTIONS" ] && [ -z "$$GITLAB_CI" ] && [ -z "$$CIRCLECI" ] && [ -z "$$JENKINS_HOME" ]; then \
				if which junit-viewer > /dev/null 2>&1; then \
					$(ECHO) "$(YELLOW)Generating HTML report for $$pkg_name...$(NC)"; \
					junit-viewer --results=../../$(TEST_REPORTS_DIR)/http-transport-$$pkg_name.xml --save=../../$(TEST_REPORTS_DIR)/http-transport-$$pkg_name.html 2>/dev/null || true; \
				fi; \
			fi; \
		done || $(ECHO) "No HTTP transport tests found"
	@$(ECHO) ""
	@if [ -z "$$CI" ] && [ -z "$$GITHUB_ACTIONS" ] && [ -z "$$GITLAB_CI" ] && [ -z "$$CIRCLECI" ] && [ -z "$$JENKINS_HOME" ]; then \
		$(ECHO) "$(CYAN)HTML reports saved to $(TEST_REPORTS_DIR)/http-transport-*.html$(NC)"; \
	else \
		$(ECHO) "$(CYAN)JUnit XML reports saved to $(TEST_REPORTS_DIR)/http-transport-*.xml$(NC)"; \
	fi

test-governance: install-gotestsum ## Run retained governance plugin tests
	@$(EXPOSE_ENV); \
	$(ECHO) "$(GREEN)Running governance plugin tests...$(NC)"; \
	mkdir -p $(TEST_REPORTS_DIR); \
	cd plugins/governance && GOWORK=off gotestsum \
		--format=$(GOTESTSUM_FORMAT) \
		--junitfile=../../$(TEST_REPORTS_DIR)/governance.xml \
		-- -v ./...

test-all: test-core test-framework test-plugins test-http-transport test test-governance ## Run retained Lite Go tests
	@$(ECHO) ""
	@$(ECHO) "$(GREEN)═══════════════════════════════════════════════════════════$(NC)"
	@$(ECHO) "$(GREEN)              All Tests Complete - Summary                 $(NC)"
	@$(ECHO) "$(GREEN)═══════════════════════════════════════════════════════════$(NC)"
	@$(ECHO) ""
	@if [ -z "$$CI" ] && [ -z "$$GITHUB_ACTIONS" ] && [ -z "$$GITLAB_CI" ] && [ -z "$$CIRCLECI" ] && [ -z "$$JENKINS_HOME" ]; then \
		$(ECHO) "$(YELLOW)Generating combined HTML report...$(NC)"; \
		junit-viewer --results=$(TEST_REPORTS_DIR) --save=$(TEST_REPORTS_DIR)/index.html 2>/dev/null || true; \
		$(ECHO) ""; \
		$(ECHO) "$(CYAN)HTML reports available in $(TEST_REPORTS_DIR)/:$(NC)"; \
		ls -1 $(TEST_REPORTS_DIR)/*.html 2>/dev/null | sed 's/^/  ✓ /' || $(ECHO) "  No reports found"; \
		$(ECHO) ""; \
		$(ECHO) "$(YELLOW)📊 View all test results:$(NC)"; \
		$(ECHO) "$(CYAN)  open $(TEST_REPORTS_DIR)/index.html$(NC)"; \
		$(ECHO) ""; \
		$(ECHO) "$(YELLOW)Or view individual reports:$(NC)"; \
		ls -1 $(TEST_REPORTS_DIR)/*.html 2>/dev/null | grep -v index.html | sed 's|$(TEST_REPORTS_DIR)/|  open $(TEST_REPORTS_DIR)/|' || true; \
		$(ECHO) ""; \
	else \
		$(ECHO) "$(CYAN)JUnit XML reports available in $(TEST_REPORTS_DIR)/:$(NC)"; \
		ls -1 $(TEST_REPORTS_DIR)/*.xml 2>/dev/null | sed 's/^/  ✓ /' || $(ECHO) "  No reports found"; \
		$(ECHO) ""; \
	fi

test-chatbot: ## Run interactive chatbot integration test (Usage: RUN_CHATBOT_TEST=1 make test-chatbot)
	@$(EXPOSE_ENV); \
	$(ECHO) "$(GREEN)Running interactive chatbot integration test...$(NC)"; \
	if [ -z "$(RUN_CHATBOT_TEST)" ]; then \
		$(ECHO) "$(YELLOW)⚠️  This is an interactive test. Set RUN_CHATBOT_TEST=1 to run it.$(NC)"; \
		$(ECHO) "$(CYAN)Usage: RUN_CHATBOT_TEST=1 make test-chatbot$(NC)"; \
		$(ECHO) ""; \
		$(ECHO) "$(YELLOW)Required environment variables:$(NC)"; \
		$(ECHO) "  - OPENAI_API_KEY (required)"; \
		$(ECHO) "  - ANTHROPIC_API_KEY (optional)"; \
		$(ECHO) "  - Additional provider keys as needed"; \
		exit 0; \
	fi; \
	cd core && RUN_CHATBOT_TEST=1 go test -v -run TestChatbot


# Linting and formatting
lint: ## Run linter for Go code
	@$(ECHO) "$(GREEN)Running golangci-lint...$(NC)"
	@golangci-lint run ./...

fmt: ## Format Go code
	@$(ECHO) "$(GREEN)Formatting Go code...$(NC)"
	@gofmt -s -w .
	@goimports -w .

format: ## Format code (Usage: make format ui)
ifeq (ui,$(filter ui,$(MAKECMDGOALS)))
	@$(ECHO) "$(GREEN)Formatting UI code...$(NC)"
	@cd ui && $(USE_NODE); npm run format
else
	@$(ECHO) "$(YELLOW)Usage: make format ui$(NC)"
endif

ui:
	@:

# Workspace helpers
setup-workspace: ## Set up Go workspace with all local modules for development
	@$(ECHO) "$(GREEN)Setting up Go workspace for local development...$(NC)"
	@$(ECHO) "$(YELLOW)Cleaning existing workspace...$(NC)"
	@rm -f go.work go.work.sum || true
	@$(ECHO) "$(YELLOW)Initializing new workspace...$(NC)"
	@go work init ./core ./framework ./plugins/compat ./plugins/governance ./plugins/logging ./plugins/modelcatalogresolver ./transports
	@$(ECHO) "$(YELLOW)Syncing workspace...$(NC)"
	@go work sync
	@$(ECHO) "$(GREEN)✓ Go workspace ready with Lite modules$(NC)"
	@$(ECHO) ""
	@$(ECHO) "$(CYAN)Local modules in workspace:$(NC)"
	@go list -m all | grep "github.com/maximhq/bifrost" | grep -v " v" | sed 's/^/  ✓ /'
	@$(ECHO) ""
	@$(ECHO) "$(CYAN)Remote modules (no local version):$(NC)"
	@go list -m all | grep "github.com/maximhq/bifrost" | grep " v" | sed 's/^/  → /'
	@$(ECHO) ""
	@$(ECHO) "$(YELLOW)Note: go.work files are not committed to version control$(NC)"

work-init: ## Create local go.work to use local modules for development (legacy)
	@$(ECHO) "$(YELLOW)⚠️  work-init is deprecated, use 'make setup-workspace' instead$(NC)"
	@$(MAKE) setup-workspace

work-clean: ## Remove local go.work
	@rm -f go.work go.work.sum || true
	@$(ECHO) "$(GREEN)Removed local go.work files$(NC)"

# Module parameter for mod-tidy (all/core/plugins/framework/transport)
MODULE ?= all

mod-tidy: ## Run go mod tidy on retained Lite modules (Usage: make mod-tidy [MODULE=all|core|plugins|framework|transport])
	@$(ECHO) "$(GREEN)Running go mod tidy...$(NC)"
	@if [ "$(MODULE)" = "all" ] || [ "$(MODULE)" = "core" ]; then \
		$(ECHO) "$(CYAN)Tidying core...$(NC)"; \
		cd core && go mod tidy && $(ECHO) "$(GREEN)  ✓ core$(NC)"; \
	fi
	@if [ "$(MODULE)" = "all" ] || [ "$(MODULE)" = "framework" ]; then \
		$(ECHO) "$(CYAN)Tidying framework...$(NC)"; \
		cd framework && go mod tidy && $(ECHO) "$(GREEN)  ✓ framework$(NC)"; \
	fi
	@if [ "$(MODULE)" = "all" ] || [ "$(MODULE)" = "transport" ]; then \
		$(ECHO) "$(CYAN)Tidying transports...$(NC)"; \
		cd transports && go mod tidy && $(ECHO) "$(GREEN)  ✓ transports$(NC)"; \
	fi
	@if [ "$(MODULE)" = "all" ] || [ "$(MODULE)" = "plugins" ]; then \
		$(ECHO) "$(CYAN)Tidying plugins...$(NC)"; \
		for plugin_dir in ./plugins/*/; do \
			if [ -d "$$plugin_dir" ] && [ -f "$$plugin_dir/go.mod" ]; then \
				plugin_name=$$(basename $$plugin_dir); \
				cd $$plugin_dir && go mod tidy && cd ../.. && $(ECHO) "$(GREEN)  ✓ plugins/$$plugin_name$(NC)"; \
			fi; \
		done; \
	fi
	@$(ECHO) ""
	@$(ECHO) "$(GREEN)✓ go mod tidy complete$(NC)"
