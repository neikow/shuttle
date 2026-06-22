import { commands, workspace, ExtensionContext, window } from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
} from "vscode-languageclient/node";
import {
  addCertificate,
  addDnsProvider,
  addHost,
  runCheck,
  runPlan,
  scaffoldService,
} from "./commands";

let client: LanguageClient | undefined;

// Shuttle's IaC files are plain YAML, so the client attaches to YAML documents
// matching the repo's known paths. `config.yml` (the orchestrator bootstrap
// config) is deliberately excluded from the auto-selector — the name is too
// generic to claim every project's config.yml — but `shuttle lsp` still handles
// it if a client sends it.
const documentSelector = [
  { language: "yaml", pattern: "**/hosts.yaml" },
  { language: "yaml", pattern: "**/services/**/*.yaml" },
  { language: "yaml", pattern: "**/dns.yml" },
  { language: "yaml", pattern: "**/orchestrator.yaml" },
];

export function activate(context: ExtensionContext): void {
  // Commands are registered unconditionally — they shell out to the `shuttle`
  // CLI and don't depend on the language server.
  context.subscriptions.push(
    commands.registerCommand("shuttle.scaffoldService", scaffoldService),
    commands.registerCommand("shuttle.addHost", addHost),
    commands.registerCommand("shuttle.addDnsProvider", addDnsProvider),
    commands.registerCommand("shuttle.addCertificate", addCertificate),
    commands.registerCommand("shuttle.check", runCheck),
    commands.registerCommand("shuttle.plan", runPlan),
  );

  startLanguageServer();
}

function startLanguageServer(): void {
  const cfg = workspace.getConfiguration("shuttle");
  if (!cfg.get<boolean>("lsp.enable", true)) {
    return;
  }
  const command = cfg.get<string>("lsp.path", "shuttle");

  const run = { command, args: ["lsp"], transport: TransportKind.stdio };
  const serverOptions: ServerOptions = { run, debug: run };
  const clientOptions: LanguageClientOptions = { documentSelector };

  client = new LanguageClient("shuttle", "Shuttle IaC", serverOptions, clientOptions);
  client.start().catch((err: unknown) => {
    window.showErrorMessage(
      `Shuttle language server failed to start (${command} lsp): ${String(err)}`,
    );
  });
}

export function deactivate(): Thenable<void> | undefined {
  return client?.stop();
}
