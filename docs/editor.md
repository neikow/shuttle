# Editor support (language server)

Shuttle ships a language server for its IaC YAML files, so an editor can offer
**completion** and **live validation** as you write `hosts.yaml`, services,
`dns.yml`, and `orchestrator.yaml`.

It is a subcommand of the same binary:

```sh
shuttle lsp        # speaks LSP on stdin/stdout — launched by your editor, not run by hand
```

The server **reuses Shuttle's own config loader**, so the editor experience stays
in lockstep with what the orchestrator actually accepts: a field added to the
schema shows up in completion, and a key the loader would reject is flagged as you
type.

## What it provides

- **Completion**
  - field names valid at the cursor's nesting (e.g. top-level service keys, or the
    keys inside an `external:` / `backup:` block, or a `dns.yml` provider/certificate
    item), with each item's **type** and a `(required)` marker, and keys already
    present in the block filtered out;
  - enum values — `update_policy` (`rolling`/`recreate`), `delete_volumes`, a dns
    provider `type` (`ovh`), `secrets_provider`, a notification `type`, `backup`
    `engine`/`store`, … — plus `true`/`false` for any boolean field;
  - cross-file references — `host:` completes from `hosts.yaml`, `tls_certificate:`
    from `dns.yml`, and a certificate's `provider:` from the providers in the same
    file;
  - a dns provider's `credentials:` keys follow the provider `type:` (e.g. `ovh`
    offers `application_key`/`application_secret`/`consumer_key`), then each
    credential's `infisical_key`/`infisical_env`/`infisical_path` one level deeper.
- **Diagnostics**, live on every edit (single-file; it runs on the unsaved buffer):
  - unknown keys, type mismatches, and YAML syntax errors;
  - invalid enum values (e.g. `update_policy: bogus`) and missing required fields
    (a service with no `name`/`host`, a notification with no `url`, …);
  - references — a `dns.yml` certificate naming an undeclared `provider`, and
    (reading the sibling files) a service `host` not in `hosts.yaml` or a
    `tls_certificate` not in `dns.yml`. A cross-file check is skipped when its
    sibling file isn't found, so editing a service in isolation never false-flags.

Syntax highlighting is your editor's built-in YAML highlighting — the language
server adds the Shuttle-specific intelligence on top.

## Commands

The VS Code extension adds Shuttle commands to the palette (Cmd/Ctrl-Shift-P),
all under the **Shuttle:** category. The scaffolding ones gather input then run
`shuttle scaffold …` and open the result; check/plan run in a terminal:

| Command | What it does |
|---------|--------------|
| **Shuttle: Scaffold Service…** | Create `services/<name>/` for a docker, compose, or external service. |
| **Shuttle: Add Host…** | Append a host to `hosts.yaml`. |
| **Shuttle: Add DNS Provider…** | Append a DNS-challenge provider to `dns.yml` (prefills the type's credential keys). |
| **Shuttle: Add Certificate…** | Append a certificate to `dns.yml`. |
| **Shuttle: Check (validate repo)** | Run `shuttle check`. |
| **Shuttle: Plan (preview changes)** | Run `shuttle plan`. |

These shell out to the `shuttle` binary, so the generated files can never drift
from what the orchestrator accepts. See
[`shuttle scaffold`](iac-repo#scaffolding-shuttle-scaffold) for the underlying
CLI.

## VS Code

A thin client lives in [`editors/vscode/`](https://github.com/neikow/shuttle/tree/main/editors/vscode).
It launches `shuttle lsp` for you. Build and install it locally:

```sh
cd editors/vscode
npm install
npm run compile
npx @vscode/vsce package          # -> shuttle-iac-<version>.vsix
code --install-extension shuttle-iac-*.vsix
```

Requirements: the `shuttle` binary on your `PATH` (or set `shuttle.lsp.path`).
`make dev-install` builds a version-stamped binary and copies it to
`/usr/local/bin` (override with `PREFIX=…`; use `sudo` if that dir isn't
writable) — a reliable PATH location for a GUI-launched editor.

| Setting | Default | Description |
|---------|---------|-------------|
| `shuttle.path` | `shuttle` | Path to the `shuttle` binary for the CLI commands (scaffold, check, plan). |
| `shuttle.lsp.enable` | `true` | Run the language server. |
| `shuttle.lsp.path` | `shuttle` | Path to the `shuttle` binary used to run `shuttle lsp`. |

## Other editors

Any LSP-capable editor can use it — point your client at `shuttle lsp` (stdio
transport) and attach it to the IaC YAML files (`hosts.yaml`,
`services/**/*.yaml`, `dns.yml`, `orchestrator.yaml`). `config.yml` is also
understood, but isn't claimed by default because the name is generic.

stdio is the only transport. Clients that append a `--stdio` argument to select
it (e.g. `vscode-languageclient`) work as-is — `shuttle lsp` accepts the flag as
a no-op.

## Troubleshooting

- **`shuttle lsp` prints nothing when run by hand.** That's expected — it waits
  for an LSP client to send framed JSON-RPC on stdin. It isn't meant to be run
  interactively. To smoke-test the binary:

  ```sh
  printf 'Content-Length: 58\r\n\r\n{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | shuttle lsp
  ```

  A healthy server replies with a `Content-Length` header and an `initialize`
  result naming `shuttle-lsp`.

- **Is the VS Code client running?** The extension activates on opening a YAML
  file and only attaches to the IaC files above, so open e.g. a
  `services/<name>/<name>.yaml` first. The "Shuttle IaC" output channel is created
  lazily on the first log line, so a healthy idle server may show **no** channel —
  set `"shuttle.trace.server": "verbose"` to force JSON-RPC tracing into the
  channel, or just check **Developer: Show Running Extensions**.

- **Server fails to start / "unknown flag".** Make sure the `shuttle` binary
  invoked by the client is recent. On macOS a GUI-launched VS Code may not inherit
  your shell `PATH`, so set `shuttle.lsp.path` to an absolute path.
