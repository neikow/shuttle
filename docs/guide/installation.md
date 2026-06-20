# Installation

Shuttle is a single binary. Install it on your control host (to run
`shuttle orchestrator`) and on each server you want to deploy to (to run
`shuttle agent`). The same binary does both.

::: tip Just trying it out?
You don't need to install anything — the [Quickstart](/guide/quickstart) runs the
whole stack in containers from a `git clone`.
:::

## Prebuilt binary (recommended)

Grab the archive for your OS/arch from the
[latest release](https://github.com/neikow/shuttle/releases/latest):

```sh
# Linux amd64 — adjust the version/arch to match the release asset
VERSION=0.1.0
curl -sSL -o shuttle.tar.gz \
  https://github.com/neikow/shuttle/releases/download/v${VERSION}/shuttle_${VERSION}_linux_amd64.tar.gz
tar -xzf shuttle.tar.gz shuttle
sudo install shuttle /usr/local/bin/shuttle

shuttle version
```

Releases ship for linux/darwin × amd64/arm64.

### Verify the download (optional)

Releases are signed keylessly with [cosign](https://docs.sigstore.dev/) and carry
an SBOM. Verify `checksums.txt` before trusting a binary:

```sh
cosign verify-blob \
  --certificate-identity-regexp 'https://github.com/neikow/shuttle/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  checksums.txt
```

## Container image

Multi-arch images are published to GHCR. The entrypoint is the `shuttle` binary,
so pass a subcommand:

```sh
docker run --rm ghcr.io/neikow/shuttle:latest version
```

The image bundles `git` and the Docker CLI + Compose plugin, so it can run as
either an orchestrator or an agent (an agent container needs the host Docker
socket mounted). Verify an image:

```sh
cosign verify ghcr.io/neikow/shuttle:latest \
  --certificate-identity-regexp 'https://github.com/neikow/shuttle/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## From source

Needs Go 1.25+ and `git`:

```sh
go install github.com/neikow/shuttle/cmd/shuttle@latest
```

Or clone and build (this is also how the Quickstart's `make dev-up` works):

```sh
git clone https://github.com/neikow/shuttle.git
cd shuttle
make build          # -> ./shuttle
make build-ui       # -> ./shuttle WITH the embedded web UI
```

## Runtime requirements

- **Orchestrator host:** `git` (it shells out to clone/pull your IaC repo).
- **Agent host:** Docker with the Compose v2 plugin (`docker compose`). For a
  Synology NAS, the [Synology target](/operations#synology-dsm-target) uses
  Container Manager.

## Next

- [Deploy to a real host](/guide/first-deployment) — go from installed binary to a
  service running on your own server.
