# syntax=docker/dockerfile:1
#
# Multi-target image for the one-command dev cluster (`make dev-up`). One Go
# build (with the embedded UI) feeds both the orchestrator and the simulated
# remote-host images, so the binary is identical across the cluster.

# --- web bundle (embedded into the binary) ---
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- shuttle binary, with the UI embedded (-tags embedui) ---
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
ARG VERSION=dev-cluster
RUN CGO_ENABLED=0 go build -tags embedui \
	-ldflags "-X main.Version=${VERSION}" \
	-o /out/shuttle ./cmd/shuttle

# --- orchestrator: needs git (IaC sync) ---
FROM alpine:3.20 AS orchestrator
RUN apk add --no-cache ca-certificates git
COPY --from=build /out/shuttle /usr/local/bin/shuttle
ENTRYPOINT ["shuttle"]

# --- simulated remote host: an isolated Docker-in-Docker daemon + the agent ---
FROM docker:27-dind AS host
# curl + jq: self-enrollment against the control plane. compose plugin: deploys.
RUN apk add --no-cache curl jq docker-cli-compose
COPY --from=build /out/shuttle /usr/local/bin/shuttle
COPY deploy/dev/host-entrypoint.sh /usr/local/bin/host-entrypoint.sh
RUN chmod +x /usr/local/bin/host-entrypoint.sh
ENTRYPOINT ["/usr/local/bin/host-entrypoint.sh"]
