# Installation

Shuttle is a single binary. Install it on your control host (to run
`shuttle orchestrator`) and on each server you want to deploy to (to run
`shuttle agent`). The same binary does both.

::: warning Alpha software
Shuttle is in **alpha**. It is tested and usable, but the CLI, config, and HTTP
API may change between releases without a deprecation path. Pin a version for
anything you rely on, and read the [changelog](https://github.com/neikow/shuttle/blob/main/CHANGELOG.md)
before upgrading.
:::

## Quick install (recommended)

The install script detects your OS/arch, downloads the matching release, verifies
its SHA-256 checksum (and the keyless [cosign](https://docs.sigstore.dev/)
signature when `cosign` is installed), then installs the binary:

```sh
curl -sSfL https://neikow.github.io/shuttle/install | bash
```

It's configurable via environment variables:

```sh
# Pin a version (without the leading "v")
curl -sSfL https://neikow.github.io/shuttle/install | SHUTTLE_VERSION=0.4.0 bash

# Install somewhere on your PATH without sudo
curl -sSfL https://neikow.github.io/shuttle/install | SHUTTLE_INSTALL_DIR="$HOME/.local/bin" bash
```

| Variable | Default | Purpose |
|----------|---------|---------|
| `SHUTTLE_VERSION` | latest release | Version to install (no leading `v`). |
| `SHUTTLE_INSTALL_DIR` | `/usr/local/bin` | Where to install (uses `sudo` only if needed). |
| `SHUTTLE_OS` / `SHUTTLE_ARCH` | auto-detected | Override platform detection (`linux`/`darwin`, `amd64`/`arm64`). |
| `SHUTTLE_NO_VERIFY` | unset | Set to `1` to skip the cosign signature check (the checksum is still enforced). |

::: tip Just trying it out?
After installing, the [Quickstart](/guide/quickstart) gets a secure orchestrator
and a running service on this machine in ~3 minutes via `shuttle init` + `shuttle
orchestrator init` — no cloud account, no cluster.
:::

Piping a script to `bash` runs code from the network — read it first at
<https://neikow.github.io/shuttle/install> if you prefer. The manual steps below
do the same work by hand.

## Prebuilt binary (manual)

Grab the archive for your OS/arch from the
[latest release](https://github.com/neikow/shuttle/releases/latest):

```sh
# Linux amd64 — adjust the version/arch to match the release asset
VERSION=0.4.0
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
