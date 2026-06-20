import { authHeaders } from "./auth";
import type {
  CheckReport,
  ControlToken,
  CreatedToken,
  DeployRecord,
  EnrollResult,
  Host,
  Overview,
  PlanReport,
  RepoWebhook,
  WhoAmI,
} from "./types";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { ...(authHeaders() as Record<string, string>) };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new ApiError(res.status, text || res.statusText);
  }
  if (res.status === 204) return undefined as T;
  const ct = res.headers.get("Content-Type") ?? "";
  if (!ct.includes("application/json")) return undefined as T;
  return res.json() as Promise<T>;
}

const get = <T>(path: string) => request<T>("GET", path);
const post = <T>(path: string, body?: unknown) => request<T>("POST", path, body);
const del = (path: string) => request<void>("DELETE", path);

export const api = {
  // --- reads ---
  whoami: () => get<WhoAmI>("/whoami"),
  overview: () => get<Overview>("/overview"),
  deploys: (service?: string) =>
    get<DeployRecord[] | null>(
      "/deploys" + (service ? `?service=${encodeURIComponent(service)}` : ""),
    ).then((d) => d ?? []),
  hosts: () => get<Host[] | null>("/hosts").then((h) => h ?? []),
  plan: (ref?: string) =>
    get<PlanReport>("/plan" + (ref ? `?ref=${encodeURIComponent(ref)}` : "")),
  check: (ref?: string) =>
    get<CheckReport>("/check" + (ref ? `?ref=${encodeURIComponent(ref)}` : "")),
  listTokens: () => get<ControlToken[] | null>("/tokens").then((t) => t ?? []),
  listWebhooks: () => get<RepoWebhook[] | null>("/webhooks/repo").then((w) => w ?? []),

  // --- operational mutations (deploy tier) ---
  deploy: (service: string, sha: string) =>
    post<{ deploy_id: string; host?: string }>(
      `/deploy/${encodeURIComponent(service)}?sha=${encodeURIComponent(sha)}`,
    ),
  rollback: (service: string, steps = 1) =>
    post<{ deploy_id: string; target_sha: string }>(
      `/rollback?service=${encodeURIComponent(service)}&steps=${steps}`,
    ),
  prune: () => post<{ pruned: string[] | null }>("/prune"),

  // --- admin mutations ---
  createToken: (name: string, role: string) =>
    post<CreatedToken>("/tokens", { name, role }),
  revokeToken: (id: string) => del(`/tokens/${encodeURIComponent(id)}`),
  createWebhook: (service: string) => post<{ id: string }>("/webhooks/repo", { service }),
  deleteWebhook: (id: string) => del(`/webhooks/repo/${encodeURIComponent(id)}`),
  enroll: (host: string) => post<EnrollResult>("/enroll", { host }),
};

// Verifies a token against a bearer-protected endpoint. Returns true if accepted.
export async function verifyToken(): Promise<boolean> {
  const res = await fetch("/whoami", { headers: authHeaders() });
  return res.ok;
}
