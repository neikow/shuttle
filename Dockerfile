# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build \
	-ldflags "-X main.Version=${VERSION} -X main.Commit=${COMMIT}" \
	-o /out/shuttle ./cmd/shuttle

FROM alpine:3.20
# git: orchestrator IaC sync. docker-cli + compose: agent deploys.
RUN apk add --no-cache ca-certificates git docker-cli docker-cli-compose
COPY --from=build /out/shuttle /usr/local/bin/shuttle
ENTRYPOINT ["shuttle"]
