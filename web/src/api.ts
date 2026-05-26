import { authHeaders } from "./auth";
import type { CheckReport, DeployRecord, Host, Overview, PlanReport } from "./types";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function get<T>(path: string): Promise<T> {
  const res = await fetch(path, { headers: authHeaders() });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new ApiError(res.status, body || res.statusText);
  }
  return res.json() as Promise<T>;
}

export const api = {
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
};

// Verifies a token against a bearer-protected endpoint. Returns true if accepted.
export async function verifyToken(): Promise<boolean> {
  const res = await fetch("/deploys?limit=1", { headers: authHeaders() });
  return res.ok;
}
