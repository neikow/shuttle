import { execFile } from "node:child_process";
import { promisify } from "node:util";
import * as path from "node:path";
import { window, workspace } from "vscode";

const pExecFile = promisify(execFile);

// shuttleBin returns the configured path to the `shuttle` binary used for CLI
// commands (separate from `shuttle.lsp.path`, which the language client uses).
function shuttleBin(): string {
  return workspace.getConfiguration("shuttle").get<string>("path", "shuttle");
}

// repoCwd resolves the IaC repo root to operate in: the workspace folder of the
// active editor, else the first workspace folder. Returns undefined (and warns)
// when there's no folder open.
export function repoCwd(): string | undefined {
  const active = window.activeTextEditor?.document.uri;
  if (active) {
    const folder = workspace.getWorkspaceFolder(active);
    if (folder) {
      return folder.uri.fsPath;
    }
  }
  const first = workspace.workspaceFolders?.[0]?.uri.fsPath;
  if (!first) {
    window.showErrorMessage("Shuttle: open a folder (your IaC repo) first.");
  }
  return first;
}

// runShuttle runs the binary with args in cwd and returns stdout, throwing on a
// non-zero exit (the thrown error carries stderr).
export async function runShuttle(args: string[], cwd: string): Promise<string> {
  const { stdout } = await pExecFile(shuttleBin(), args, { cwd });
  return stdout;
}

// runInTerminal runs an interactive command (check/plan) in a reused "Shuttle"
// terminal so streaming output and any prompts are visible.
export function runInTerminal(args: string[], cwd: string): void {
  const term = window.createTerminal({ name: "Shuttle", cwd });
  term.show();
  term.sendText([shuttleBin(), ...args].join(" "));
}

// extractPaths pulls the file paths out of scaffold output ("created <p>" /
// "updated <p>" lines), resolved absolute against cwd.
export function extractPaths(stdout: string, cwd: string): string[] {
  const out: string[] = [];
  for (const line of stdout.split("\n")) {
    const m = /^(?:created|updated) (.+)$/.exec(line.trim());
    if (m) {
      out.push(path.isAbsolute(m[1]) ? m[1] : path.join(cwd, m[1]));
    }
  }
  return out;
}

// errorText prefers a failed process's stderr, falling back to its message.
export function errorText(e: unknown): string {
  const err = e as { stderr?: string; message?: string };
  return err.stderr?.trim() || err.message || String(e);
}
