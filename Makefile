# Makefile for common development tasks

.PHONY: help db-up db-down db-restart db-shell health-check db-health db-query llm-check all-checks logs

SHELL := /bin/bash
SCRIPT_DIR := scripts

# PowerShell detection for Windows
ifeq ($(OS),Windows_NT)
	RUN = powershell -Command "& './$<'"
	PS = 1
else
	RUN = bash $<
	PS = 0
endif

help:
	@echo "PA Development Tasks"
	@echo ""
	@echo "Database Management:"
	@echo "  make db-up          Start PostgreSQL container"
	@echo "  make db-down        Stop PostgreSQL container"
	@echo "  make db-restart     Restart PostgreSQL"
	@echo "  make db-shell       Open psql interactive shell"
	@echo "  make db-health      Check database health and stats"
	@echo "  make db-query       View and execute database queries"
	@echo ""
	@echo "System Checks:"
	@echo "  make health-check   Check all services (database, server, Ollama)"
	@echo "  make all-checks     Complete diagnostic check"
	@echo "  make llm-check      Verify LLM provider setup"
	@echo ""
	@echo "Logs:"
	@echo "  make logs           Show container logs (Ctrl+C to exit)"
	@echo ""
	@echo "Examples:"
	@echo "  make db-up && make all-checks"
	@echo "  make db-shell"
	@echo "  make db-query Q=schema"
	@echo ""

# Database commands
db-up:
	@echo "Starting PostgreSQL..."
ifeq ($(PS),1)
	@powershell -Command "& '$(SCRIPT_DIR)/container.ps1' -Command up"
else
	@bash $(SCRIPT_DIR)/container.ps1 up
endif

db-down:
	@echo "Stopping PostgreSQL..."
ifeq ($(PS),1)
	@powershell -Command "& '$(SCRIPT_DIR)/container.ps1' -Command down"
else
	@bash $(SCRIPT_DIR)/container.ps1 down
endif

db-restart:
	@echo "Restarting PostgreSQL..."
ifeq ($(PS),1)
	@powershell -Command "& '$(SCRIPT_DIR)/container.ps1' -Command restart"
else
	@bash $(SCRIPT_DIR)/container.ps1 restart
endif

db-shell:
ifeq ($(PS),1)
	@powershell -Command "& '$(SCRIPT_DIR)/container.ps1' -Command shell"
else
	@psql -h localhost -p 5433 -U pa -d pa
endif

db-health:
ifeq ($(PS),1)
	@powershell -Command "& '$(SCRIPT_DIR)/db-health.ps1'"
else
	@bash $(SCRIPT_DIR)/db-health.sh
endif

# Database query helper
# Usage: make db-query Q=schema E=1
# Q = query name (default: help), E = execute (optional)
Q ?= help
E ?= 0
db-query:
ifeq ($(PS),1)
	@if [ "$(E)" = "1" ]; then \
		powershell -Command "& '$(SCRIPT_DIR)/db-query.ps1' -Query '$(Q)' -Execute"; \
	else \
		powershell -Command "& '$(SCRIPT_DIR)/db-query.ps1' -Query '$(Q)'"; \
	fi
else
	@echo "Query helper not available in bash mode"
	@echo "Use: psql -h localhost -p 5433 -U pa -d pa"
endif

# System checks
health-check:
ifeq ($(PS),1)
	@powershell -Command "& '$(SCRIPT_DIR)/health-check.ps1'"
else
	@bash $(SCRIPT_DIR)/health-check.sh
endif

all-checks:
ifeq ($(PS),1)
	@powershell -Command "& '$(SCRIPT_DIR)/all-checks.ps1'"
else
	@echo "all-checks.ps1 not available in bash mode"
	@echo "Run individual scripts: bash scripts/health-check.sh"
endif

llm-check:
ifeq ($(PS),1)
	@powershell -Command "& '$(SCRIPT_DIR)/llm-check.ps1'"
else
	@echo "LLM check not available in bash mode"
	@echo "Check: curl http://localhost:11434/api/tags"
endif

logs:
ifeq ($(PS),1)
	@powershell -Command "& '$(SCRIPT_DIR)/container.ps1' -Command logs"
else
	@docker compose logs -f || podman compose logs -f
endif

# Development tasks
run-server:
	@echo "Starting PA server..."
	@go run ./cmd/pa

run-cli:
	@echo "Building PA CLI..."
	@go build -o bin/pa ./cmd/pa-cli
	@echo "CLI built to bin/pa"

test:
	@echo "Running tests..."
	@go test -v ./...

fmt:
	@echo "Formatting code..."
	@go fmt ./...

lint:
	@echo "Running linter..."
	@which golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not found"

.PHONY: run-server run-cli test fmt lint
