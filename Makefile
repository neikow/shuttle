.PHONY: build build-ui web web-install web-dev web-test web-check docs docs-install docs-dev docs-build test test-unit test-integration lint proto clean dev-repo dev-clean dev-gitea dev-gitea-setup dev-gitea-clean dev-gitea-webhook-setup

BINARY := shuttle
MODULE := github.com/neikow/shuttle
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -X main.Version=$(VERSION) -X main.Commit=$(COMMIT)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/shuttle

# Build the binary with the embedded web UI (runs the frontend build first).
build-ui: web
	go build -tags embedui -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/shuttle

# Install the production frontend deps.
web-install:
	cd web && npm ci

# Build the React UI into web/dist (consumed by the embedui build tag).
web: web-install
	cd web && npm run build

# Run the Vite dev server (proxies API calls to a local orchestrator on :8080).
web-dev:
	cd web && npm run dev

# Run the frontend unit/component tests (Vitest + React Testing Library).
web-test: web-install
	cd web && npm run test

# Typecheck + build the frontend (catches TS + bundling errors in CI).
web-check: web-install
	cd web && npm run build

# Install the docs site deps (VitePress).
docs-install:
	cd docs && npm ci

# Run the VitePress dev server (live-reload docs at http://localhost:5173/shuttle/).
docs-dev: docs-install
	cd docs && npm run docs:dev

# Build the static docs site into docs/.vitepress/dist.
docs-build: docs-install
	cd docs && npm run docs:build

# Alias: build the docs site.
docs: docs-build

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/shuttle

test-unit:
	go test -count=1 -race ./internal/...

test-integration:
	go test -count=1 -race -tags integration ./test/integration/...

test: test-unit

lint:
	golangci-lint run ./...

vet:
	go vet ./...

proto:
	buf generate

proto-lint:
	buf lint

proto-breaking:
	buf breaking --against '.git#branch=main'

clean:
	rm -f $(BINARY)

# Generate mTLS dev certs (CA + orchestrator + one agent) for local testing
certs:
	mkdir -p certs
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -days 3650 -nodes \
		-keyout certs/ca.key -out certs/ca.crt -subj "/CN=shuttle-dev-ca"
	openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
		-keyout certs/orchestrator.key -out certs/orchestrator.csr -subj "/CN=orchestrator" \
		-addext "subjectAltName=DNS:orchestrator,DNS:localhost,IP:127.0.0.1"
	openssl x509 -req -in certs/orchestrator.csr -CA certs/ca.crt -CAkey certs/ca.key \
		-CAcreateserial -out certs/orchestrator.crt -days 3650 -copy_extensions copyall
	openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
		-keyout certs/agent.key -out certs/agent.csr -subj "/CN=agent-dev"
	openssl x509 -req -in certs/agent.csr -CA certs/ca.crt -CAkey certs/ca.key \
		-CAcreateserial -out certs/agent.crt -days 3650

DEV_CLUSTER_DIR := .dev-cluster
DEV_COMPOSE := deploy/docker-compose.dev.yml

# One-command dev cluster: orchestrator + embedded UI + two SIMULATED REMOTE
# HOSTS, each an isolated Docker-in-Docker daemon running a self-enrolling agent
# that deploys into its own engine. Seeds an editable IaC git repo on first run.
# UI: http://localhost:8080/ui/  (bearer token: test-bearer)
dev-up:
	@mkdir -p $(DEV_CLUSTER_DIR)
	@if [ ! -d $(DEV_CLUSTER_DIR)/iac/.git ]; then \
		rm -rf $(DEV_CLUSTER_DIR)/iac; \
		cp -r deploy/dev/iac $(DEV_CLUSTER_DIR)/iac; \
		git -C $(DEV_CLUSTER_DIR)/iac init -q -b main; \
		git -C $(DEV_CLUSTER_DIR)/iac add -A; \
		git -C $(DEV_CLUSTER_DIR)/iac -c user.email=dev@shuttle.local -c user.name=shuttle-dev commit -qm "seed dev cluster IaC"; \
		echo "seeded $(DEV_CLUSTER_DIR)/iac"; \
	else \
		echo "$(DEV_CLUSTER_DIR)/iac already exists; leaving your edits intact"; \
	fi
	docker compose -f $(DEV_COMPOSE) up --build -d
	@echo ""
	@echo "Dev cluster up."
	@echo "  UI:    http://localhost:8080/ui/   (paste token: test-bearer)"
	@echo "  Hosts: web1, web2 — each an isolated Docker-in-Docker engine"
	@echo "  Edit $(DEV_CLUSTER_DIR)/iac and commit; the reconciler deploys within ~60s."
	@echo "  Logs:  make dev-logs     Tear down: make dev-down"

# Follow logs from every cluster container.
dev-logs:
	docker compose -f $(DEV_COMPOSE) logs -f

# Tear the cluster down and remove its volumes + seeded repo.
dev-down:
	docker compose -f $(DEV_COMPOSE) down -v
	rm -rf $(DEV_CLUSTER_DIR)

# Start the Gitea dev container for testing private repo auth.
dev-gitea:
	docker compose -f deploy/docker-compose.gitea.yml up -d
	@echo "Gitea running at http://localhost:3000"
	@echo "Run 'make dev-gitea-setup' to create the test repo"

# Provision the Gitea test user, private repo, and push seed fixtures.
# Prints the token to store as GITEA_TOKEN in Infisical.
dev-gitea-setup: dev-gitea
	bash deploy/gitea-setup.sh

# Stop and remove the Gitea dev container and its volumes.
dev-gitea-clean:
	docker compose -f deploy/docker-compose.gitea.yml down -v

# Provision Gitea test repo and register a repo webhook with the orchestrator.
# Requires Gitea running (make dev-gitea) and the orchestrator to be up.
dev-gitea-webhook-setup:
	bash deploy/gitea-webhook-setup.sh
