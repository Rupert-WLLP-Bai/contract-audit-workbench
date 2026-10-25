.PHONY: help setup db-up db-down dev worker tool-gateway migrate web web-client web-admin web-portal test-unit verify acceptance browser-distributed test-live evaluate browser-install infra-bootstrap infra-config infra-up infra-down infra-smoke infra-stop app-bootstrap app-config app-preflight app-images-offline apps-up apps-up-fixture apps-down agent-demo rule-shadow decision-baseline
PYTHON ?= python3
GO ?= go
BUN ?= bun
DATABASE_TARGET ?= development
PLANNER_MODE ?=

help:
	@echo "setup             install locked Go/web dependencies"
	@echo "infra-bootstrap   generate private infrastructure and distributed app credentials"
	@echo "infra-config      validate both local Compose files without starting services"
	@echo "infra-up          start PostgreSQL + RustFS + NATS and preserve volumes"
	@echo "infra-down        stop local infrastructure, preserving volumes"
	@echo "infra-smoke       real SQL/S3 round-trip checks (requires running Docker)"
	@echo "infra-stop        stop PostgreSQL + RustFS, retain volumes"
	@echo "app-bootstrap     generate private per-service DB and bearer credentials"
	@echo "app-preflight     check ports, isolated SQL/S3, OIDC, toolchain and app images"
	@echo "app-images-offline package current Go binaries and three web entrypoints from cached bases"
	@echo "apps-up           migrate and start one replica of each service, real model profile"
	@echo "apps-up-fixture   migrate and start the explicitly labeled local model fixture"
	@echo "DATABASE_TARGET=development|test selects the isolated app database"
	@echo "PLANNER_MODE=fixed_chain|deterministic|fixture|live overrides the app review planner"
	@echo "dev / web         run API / Vite in separate terminals"
	@echo "worker / tool-gateway  run independent execution and read-only tool processes"
	@echo "                  (need SERVICE_ROLE, the service *_DATABASE_URL, HEALTH_ADDR for the worker, and GATEWAY_ROLE_TOKEN for the tool gateway)"
	@echo "migrate           one-shot schema and service-role migration"
	@echo "web-client / web-admin / web-portal  build independent static entry artifacts"
	@echo "test-unit         run pure Go tests without database or model"
	@echo "verify            format, vet, race, coverage, HTTP system tests, web build, evaluator"
	@echo "acceptance        verify plus real Chromium user journey (release gate)"
	@echo "browser-distributed  drive the running distributed stack (client+admin+portal) in Edge"
	@echo "test-live         opt-in external model test, requires configured endpoint"
	@echo "evaluate          generate deterministic synthetic evaluation JSON"
	@echo "agent-demo        run six synthetic Eino control-regression scenarios"
	@echo "rule-shadow       synthetic Eino RulePack experiment, decision disabled by default"
	@echo "decision-baseline real configured chat model on fixed Chinese synthetic decisions"

setup:
	$(GO) mod download
	cd web && $(BUN) install --frozen-lockfile

db-up:
	$(PYTHON) deploy/scripts/infra.py up

db-down:
	$(PYTHON) deploy/scripts/infra.py stop

infra-bootstrap:
	$(PYTHON) deployments/local.py bootstrap --database-target $(DATABASE_TARGET)

infra-config:
	$(PYTHON) deployments/local.py config --database-target $(DATABASE_TARGET)

infra-up:
	$(PYTHON) deployments/local.py infra-up --database-target $(DATABASE_TARGET) $(if $(IDENTITY),--identity,)

infra-down:
	$(PYTHON) deployments/local.py infra-down --database-target $(DATABASE_TARGET) $(if $(IDENTITY),--identity,)

infra-smoke:
	$(PYTHON) deploy/scripts/infra.py smoke

infra-stop:
	$(PYTHON) deployments/local.py infra-down --database-target $(DATABASE_TARGET) $(if $(IDENTITY),--identity,)

app-bootstrap:
	$(PYTHON) deployments/local.py bootstrap --database-target $(DATABASE_TARGET)

app-config:
	$(PYTHON) deployments/local.py config --database-target $(DATABASE_TARGET)

app-preflight:
	$(PYTHON) deployments/local.py preflight --database-target $(DATABASE_TARGET)

app-images-offline:
	$(PYTHON) -I deployments/scripts/package-app-images.py

apps-up:
	$(PYTHON) deployments/local.py apps-up --database-target $(DATABASE_TARGET) $(if $(PLANNER_MODE),--planner-mode $(PLANNER_MODE),) $(if $(OFFLINE_IMAGES),--offline-images,)

apps-up-fixture:
	$(PYTHON) deployments/local.py apps-up-fixture --database-target $(DATABASE_TARGET) $(if $(PLANNER_MODE),--planner-mode $(PLANNER_MODE),) $(if $(OFFLINE_IMAGES),--offline-images,)

apps-down:
	$(PYTHON) deployments/local.py apps-down --database-target $(DATABASE_TARGET)

dev:
	$(GO) run ./cmd/server

worker:
	$(GO) run ./cmd/worker

tool-gateway:
	$(GO) run ./cmd/toolgateway

migrate:
	$(GO) run ./cmd/migrate

web-client:
	cd web && $(BUN) run build:client

web-admin:
	cd web && $(BUN) run build:admin

web-portal:
	cd web && $(BUN) run build:portal

web:
	cd web && $(BUN) run dev --host 127.0.0.1

test-unit:
	$(GO) test -race -count=1 ./internal/access ./internal/agentflow ./internal/decision ./internal/rulepack ./internal/telecom ./internal/identity ./internal/objectstore ./internal/accounts ./internal/audit ./internal/config ./internal/docir ./internal/docversion ./internal/engine ./internal/fault ./internal/labels ./internal/model ./internal/revise ./internal/compare ./cmd/eval ./cmd/agentdemo

verify:
	$(PYTHON) scripts/verify.py

acceptance:
	$(PYTHON) scripts/verify.py --browser

browser-distributed:
	$(GO) test -tags "browser distributed" -count=1 -timeout=900s -v ./e2e -run '^TestBrowserDistributed'

# Opt-in real model + real PostgreSQL + standard Playwright functional suite.
test-functional:
	$(GO) test -tags "browser live" -count=1 -timeout=1800s -v ./e2e -run '^TestBrowserFunctionalLive$$'

browser-install:
	cd web && $(BUN) x playwright install chromium

test-live:
	$(GO) test -tags live -count=1 -v ./internal/httpapi -run '^TestLive'

evaluate:
	$(GO) run ./cmd/eval -out artifacts/evaluation.json

agent-demo:
	$(GO) run ./cmd/agentdemo -out artifacts/agent-demo.json

rule-shadow:
	$(GO) run ./cmd/ruleshadow -publish-demo -out artifacts/ruleshadow-disabled.json

decision-baseline:
	$(GO) run ./cmd/ruleshadow -publish-demo -mode shadow -backend chat -chat-url http://127.0.0.1:3425/v1 -out artifacts/ruleshadow-chat.json
