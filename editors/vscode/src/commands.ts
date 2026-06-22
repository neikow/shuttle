import { window, workspace } from "vscode";
import {
  errorText,
  extractPaths,
  repoCwd,
  runInTerminal,
  runShuttle,
} from "./cli";

// nameRule validates a service/host/provider/certificate name.
function nameRule(v: string): string | undefined {
  return /^[a-z0-9][a-z0-9._-]*$/i.test(v.trim())
    ? undefined
    : "use letters, digits, '.', '-', '_'";
}

function numRule(v: string): string | undefined {
  if (v.trim() === "") return undefined; // optional
  return /^\d+$/.test(v.trim()) ? undefined : "must be a number";
}

interface InputOpts {
  placeHolder?: string;
  value?: string;
  required?: boolean;
  validate?: (v: string) => string | undefined;
}

// ask prompts for a line of text. Returns undefined when the user cancels (so
// callers abort), or the trimmed value.
async function ask(prompt: string, opts: InputOpts = {}): Promise<string | undefined> {
  const v = await window.showInputBox({
    prompt,
    placeHolder: opts.placeHolder,
    value: opts.value,
    ignoreFocusOut: true,
    validateInput: (raw) => {
      const t = raw.trim();
      if (opts.required && t === "") return "required";
      return opts.validate?.(t);
    },
  });
  return v === undefined ? undefined : v.trim();
}

async function pick(items: string[], placeHolder: string): Promise<string | undefined> {
  return window.showQuickPick(items, { placeHolder, ignoreFocusOut: true });
}

function splitCsv(s: string): string[] {
  return s
    .split(",")
    .map((x) => x.trim())
    .filter((x) => x !== "");
}

// runAndOpen runs a scaffold command, then opens the file it created/updated and
// reports the outcome.
async function runAndOpen(args: string[], cwd: string): Promise<void> {
  try {
    const out = await runShuttle(args, cwd);
    const [file] = extractPaths(out, cwd);
    window.showInformationMessage(`Shuttle: ${args[1]} ${args[2]} ✓`);
    if (file) {
      const doc = await workspace.openTextDocument(file);
      await window.showTextDocument(doc);
    }
  } catch (e) {
    window.showErrorMessage(`Shuttle ${args[0]} ${args[1]} failed: ${errorText(e)}`);
  }
}

export async function scaffoldService(): Promise<void> {
  const cwd = repoCwd();
  if (!cwd) return;

  const name = await ask("Service name", { required: true, validate: nameRule });
  if (!name) return;
  const kind = await pick(["docker", "compose", "external"], "Service kind");
  if (!kind) return;
  const host = await ask("Host the service runs on", { required: true });
  if (!host) return;

  const domainsRaw = await ask(
    kind === "external"
      ? "Domains to route (comma-separated, required)"
      : "Domains to route (comma-separated, optional)",
    { required: kind === "external", placeHolder: "app.example.com" },
  );
  if (domainsRaw === undefined) return;

  const args = ["scaffold", "service", name, "--repo", cwd, "--host", host, "--kind", kind];
  for (const d of splitCsv(domainsRaw)) args.push("--domain", d);

  if (kind === "external") {
    const upstream = await ask("Upstream address (host:port)", {
      required: true,
      placeHolder: "host.docker.internal:8080",
    });
    if (!upstream) return;
    args.push("--upstream", upstream);
  } else if (kind === "docker") {
    const image = await ask("Container image (optional)", { placeHolder: "nginx:latest" });
    if (image === undefined) return;
    if (image) args.push("--image", image);
  }

  const port = await ask("Traffic port (optional)", { validate: numRule, placeHolder: "8080" });
  if (port === undefined) return;
  if (port) args.push("--port", port);

  await runAndOpen(args, cwd);
}

export async function addHost(): Promise<void> {
  const cwd = repoCwd();
  if (!cwd) return;

  const name = await ask("Host name", { required: true, validate: nameRule });
  if (!name) return;
  const labelsRaw = await ask("Labels (comma-separated key=value, optional)", {
    placeHolder: "region=eu, role=edge",
  });
  if (labelsRaw === undefined) return;

  const args = ["scaffold", "host", name, "--repo", cwd];
  for (const kv of splitCsv(labelsRaw)) args.push("--label", kv);
  await runAndOpen(args, cwd);
}

export async function addDnsProvider(): Promise<void> {
  const cwd = repoCwd();
  if (!cwd) return;

  const name = await ask("Provider name", { required: true, validate: nameRule });
  if (!name) return;
  const type = await pick(["ovh"], "Provider type");
  if (!type) return;
  const endpoint = await ask("Endpoint (optional)", {
    placeHolder: type === "ovh" ? "ovh-eu" : "",
  });
  if (endpoint === undefined) return;

  const args = ["scaffold", "dns-provider", name, "--repo", cwd, "--type", type];
  if (endpoint) args.push("--endpoint", endpoint);
  await runAndOpen(args, cwd);
}

export async function addCertificate(): Promise<void> {
  const cwd = repoCwd();
  if (!cwd) return;

  const name = await ask("Certificate name", { required: true, validate: nameRule });
  if (!name) return;
  const provider = await ask("DNS provider name", { required: true, validate: nameRule });
  if (!provider) return;
  const domainsRaw = await ask("Subject domains (comma-separated, required)", {
    required: true,
    placeHolder: "*.example.com, example.com",
  });
  if (!domainsRaw) return;

  const args = ["scaffold", "certificate", name, "--repo", cwd, "--provider", provider];
  for (const d of splitCsv(domainsRaw)) args.push("--domain", d);
  await runAndOpen(args, cwd);
}

export function runCheck(): void {
  const cwd = repoCwd();
  if (cwd) runInTerminal(["check"], cwd);
}

export function runPlan(): void {
  const cwd = repoCwd();
  if (cwd) runInTerminal(["plan"], cwd);
}
