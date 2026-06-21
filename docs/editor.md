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
    item);
  - enum values — `update_policy` (`rolling`/`recreate`), `delete_volumes`, a dns
    provider `type` (`ovh`), `secrets_provider`, a notification `type`, …;
  - cross-file references — `host:` completes from `hosts.yaml`, `tls_certificate:`
    from `dns.yml`, and a certificate's `provider:` from the providers in the same
    file.
- **Diagnostics** — unknown keys, type mismatches, and YAML syntax errors, live on
  every edit (single-file; it runs on the unsaved buffer).

Syntax highlighting is your editor's built-in YAML highlighting — the language
server adds the Shuttle-specific intelligence on top.

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

| Setting | Default | Description |
|---------|---------|-------------|
| `shuttle.lsp.enable` | `true` | Run the language server. |
| `shuttle.lsp.path` | `shuttle` | Path to the `shuttle` binary. |

## Other editors

Any LSP-capable editor can use it — point your client at `shuttle lsp` (stdio
transport) and attach it to the IaC YAML files (`hosts.yaml`,
`services/**/*.yaml`, `dns.yml`, `orchestrator.yaml`). `config.yml` is also
understood, but isn't claimed by default because the name is generic.

stdio is the only transport. Clients that append a `--stdio` argument to select
it (e.g. `vscode-languageclient`) work as-is — `shuttle lsp` accepts the flag as
a no-op.
