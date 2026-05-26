.PHONY: build build-ui web web-install web-dev test test-unit test-integration lint proto clean dev-repo dev-clean dev-gitea dev-gitea-setup dev-gitea-clean dev-gitea-webhook-setup

BINARY := shuttle
MODULE := github.com/neikow/shuttle
DEV_DIR := .dev
AGENT_WORK_DIR = agent-work
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

# Scaffold a local git-driven dev environment: an editable IaC repo seeded from
# examples/repo plus an orchestrator config wired to it (server TLS + token auth).
# Re-runnable: it preserves an existing .dev/iac so your edits survive.
dev-repo: build
	@[ -f certs/ca.crt ] || $(MAKE) certs
	@mkdir -p $(DEV_DIR)
	@if [ ! -d $(DEV_DIR)/iac/.git ]; then \
		rm -rf $(DEV_DIR)/iac; \
		cp -r examples/repo $(DEV_DIR)/iac; \
		git -C $(DEV_DIR)/iac init -q -b main; \
		git -C $(DEV_DIR)/iac add -A; \
		git -C $(DEV_DIR)/iac -c user.email=dev@shuttle.local -c user.name=shuttle-dev commit -qm "seed dev IaC repo"; \
		echo "scaffolded $(DEV_DIR)/iac (git repo seeded from examples/repo)"; \
	else \
		echo "$(DEV_DIR)/iac already exists; leaving your edits intact"; \
	fi
	@sed -e "s|__REPO_URL__|file://$(abspath $(DEV_DIR))/iac|" \
	     -e "s|__CERT_DIR__|$(abspath certs)|" \
	     -e "s|__DEV_DIR__|$(abspath $(DEV_DIR))|" \
	     deploy/config.dev.example.yml > $(DEV_DIR)/orchestrator.yml
	@echo "wrote $(DEV_DIR)/orchestrator.yml"
	@echo ""
	@echo "Start the orchestrator:"
	@echo "  ./$(BINARY) orchestrator --config $(DEV_DIR)/orchestrator.yml"
	@echo "Enroll an agent (new terminal), then run the printed command + --ca certs/ca.crt:"
	@echo "  ./$(BINARY) enroll --url http://127.0.0.1:8099 --token test-bearer"
	@echo "  # add --caddy to the agent command to run a managed ingress sidecar"
	@echo ""
	@echo "Edit $(DEV_DIR)/iac (hosts.yaml, services/<name>/) and commit;"
	@echo "the reconciler picks up new commits within ~60s. List deploys:"
	@echo "  curl -s -H 'Authorization: Bearer test-bearer' http://127.0.0.1:8099/deploys"

# Remove the scaffolded dev environment (repo, config, ledger data).
dev-clean:
	rm -rf $(DEV_DIR)
	rm -rf $(AGENT_WORK_DIR)

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
