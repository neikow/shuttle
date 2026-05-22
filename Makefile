.PHONY: build test test-unit test-integration lint proto clean

BINARY := shuttle
MODULE := github.com/neikow/shuttle
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
		-keyout certs/orchestrator.key -out certs/orchestrator.csr -subj "/CN=orchestrator"
	openssl x509 -req -in certs/orchestrator.csr -CA certs/ca.crt -CAkey certs/ca.key \
		-CAcreateserial -out certs/orchestrator.crt -days 3650
	openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
		-keyout certs/agent.key -out certs/agent.csr -subj "/CN=agent-dev"
	openssl x509 -req -in certs/agent.csr -CA certs/ca.crt -CAkey certs/ca.key \
		-CAcreateserial -out certs/agent.crt -days 3650
