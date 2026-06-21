# Shuttle IaC — VS Code extension

Schema-aware completion and live validation for [Shuttle](https://github.com/neikow/shuttle)
Infrastructure-as-Code files:

- `hosts.yaml`
- `services/<name>/<name>.yaml`
- `dns.yml`
- `orchestrator.yaml`

It is a thin client: it launches `shuttle lsp` (a subcommand of the `shuttle`
binary) as a language server over stdio. All the schema knowledge lives in the
server, which reuses Shuttle's own config loader — so completion and diagnostics
stay in lockstep with what the orchestrator actually accepts.

## What you get

- **Completion** — field names valid at the cursor's nesting (driven by the Go
  config structs), enum values (`update_policy`, `delete_volumes`, dns provider
  `type`, …), and cross-file references (`host:` → names from `hosts.yaml`,
  `tls_certificate:` → certificates from `dns.yml`, a dns certificate's
  `provider:` → providers in the same file).
- **Diagnostics** — unknown keys, type mismatches, and YAML syntax errors, live
  as you type.

Syntax highlighting is VS Code's built-in YAML highlighting; this extension adds
the Shuttle-specific intelligence on top.

## Requirements

The `shuttle` binary must be installed and on your `PATH` (or set
`shuttle.lsp.path` to its absolute path). Verify with `shuttle lsp --help`.

## Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `shuttle.lsp.enable` | `true` | Run the Shuttle language server. |
| `shuttle.lsp.path` | `shuttle` | Path to the `shuttle` binary. |

## Build / package locally

```sh
cd editors/vscode
npm install
npm run compile           # type-check + emit out/
npx @vscode/vsce package  # -> shuttle-iac-<version>.vsix
code --install-extension shuttle-iac-*.vsix
```

`config.yml` (the orchestrator bootstrap config) is intentionally **not** in the
default file selector — the name is too generic — but `shuttle lsp` validates it
too if your editor is configured to send it.
