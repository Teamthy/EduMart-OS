.DEFAULT_GOAL := help
SHELL := /bin/bash

.PHONY: help install install-mobile dev dev-api dev-web dev-mobile test test-api test-postgres test-web typecheck contract assets smoke drill backup pitr build build-api fmt vet clean secrets audit security

help: ## Show available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

install: ## Install web + shared dependencies
	npm install

install-mobile: ## Install Expo dependencies (large, separate from the workspace)
	npm run install:mobile

dev: dev-api ## Run the API (and print the web command)

dev-api: ## Run the Go API on :8080 (SQLite at apps/api/data)
	cd apps/api && go run ./cmd/api

dev-web: ## Run the Next.js app on :3000
	npm run dev

dev-mobile: ## Run the Expo dev server
	npm run dev:mobile

test: test-api test-web ## Run all tests

test-api: ## Go tests with the race detector (SQLite)
	cd apps/api && go test ./... -race -count=1

test-postgres: ## Go tests against PostgreSQL (needs infra up and EDUMART_TEST_POSTGRES_DSN)
	cd apps/api && EDUMART_TEST_POSTGRES_DSN="$${EDUMART_TEST_POSTGRES_DSN:-postgres://edumart:local_only_change_me@localhost:5432/edumart_test?sslmode=disable}" go test ./... -race -count=1

test-web: ## Vitest
	npm run test --workspace @edumart/web

typecheck: ## Typecheck shared + web (+ mobile when installed)
	npm run typecheck

backup: ## Prove the backup strategy: live snapshot, tamper detection, retention, off-site copy
	python3 scripts/backup-drill.py sqlite

pitr: ## Prove point-in-time recovery (needs PostgreSQL server binaries: initdb, pg_basebackup)
	python3 scripts/pitr-drill.py

secrets: ## Scan the tracked tree for committed secrets (with a self-test first)
	python3 scripts/scan-secrets.py --self-test && python3 scripts/scan-secrets.py

audit: ## Fail on a reachable high-severity advisory in production JavaScript dependencies
	npm audit --omit=dev --audit-level=high

security: secrets audit ## Everything CI's Security job runs that works locally (govulncheck needs network)
	@echo "govulncheck: run 'cd apps/api && govulncheck ./...' where the Go vulnerability database is reachable"

contract: ## Check the OpenAPI contract against the Go router
	python3 scripts/check-contract.py

infra: ## Start local PostgreSQL + Redis (optional; the API defaults to SQLite)
	docker compose -f infra/docker/compose.yml up -d

smoke: ## Walk the learner journey against a running API (BASE=http://localhost:8080)
	python3 scripts/smoke.py $(or $(BASE),http://localhost:8080)

drill: ## Destroy a database and prove the backup brings it back (ENGINE=sqlite|postgres|both)
	python3 scripts/restore-drill.py $(or $(ENGINE),sqlite) $(if $(DSN),--dsn $(DSN))

assets: ## Regenerate the app icons and splash
	python3 scripts/render-assets.py

fmt: ## Format Go sources
	cd apps/api && gofmt -w .

vet: ## Vet Go sources
	cd apps/api && go vet ./...

build: build-api ## Build the API binary and the web app
	npm run build

build-api: ## Build the API binary to dist/
	cd apps/api && go build -o ../../dist/edumart-api ./cmd/api

clean: ## Remove build output and local databases
	rm -rf dist apps/web/.next apps/api/data
