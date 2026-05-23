.PHONY: build test test-unit test-integration lint proto clean dev-repo dev-clean

BINARY := shuttle
MODULE := github.com/neikow/shuttle
DEV_DIR := .dev
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -X main.Version=$(VERSION) -X main.Commit=$(COMMIT)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/shuttle

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
	@echo ""
	@echo "Edit $(DEV_DIR)/iac (hosts.yaml, services/<name>/) and commit;"
	@echo "the reconciler picks up new commits within ~60s. List deploys:"
	@echo "  curl -s -H 'Authorization: Bearer test-bearer' http://127.0.0.1:8099/deploys"

# Remove the scaffolded dev environment (repo, config, ledger data).
dev-clean:
	rm -rf $(DEV_DIR)
