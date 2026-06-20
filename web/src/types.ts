// Mirrors the orchestrator's JSON. DeployRecord has no JSON tags server-side, so
// it marshals with Go field names (PascalCase) — matched here verbatim.
export interface DeployRecord {
  DeployID: string;
  Service: string;
  Host: string;
  SHA: string;
  Status: string;
  TriggeredBy: string;
  WebhookNonce: string;
  StartedAt: string;
}

// DeployLog is one captured output line of a deploy/rollback (GET
// /deploys/{id}/logs). Marshals with the ledger's snake_case JSON tags.
export interface DeployLog {
  at: string;
  stream: string;
  text: string;
}

export type PlanAction = "create" | "update" | "unchanged" | "remove";

export interface PlanEntry {
  service: string;
  host?: string;
  action: PlanAction;
  current_sha?: string;
  desired_sha?: string;
}

export interface PlanReport {
  sha: string;
  services: PlanEntry[];
}

export interface ServiceCheck {
  service: string;
  env?: string;
  base_path?: string;
  service_path?: string;
  schema?: string[];
  missing_keys?: string[];
  warnings?: string[];
  error?: string;
}

export interface GitCredentialCheck {
  repo_prefix: string;
  key: string;
  error?: string;
}

export interface CheckReport {
  sha: string;
  has_provider: boolean;
  git_credentials?: GitCredentialCheck[];
  services?: ServiceCheck[];
}

export interface Host {
  name: string;
  labels?: Record<string, string>;
}

export interface ServiceState {
  service: string;
  status: string;
  sha?: string;
  reported_at: string;
}

export interface OverviewHost {
  name: string;
  connected: boolean;
  last_seen?: string;
  services: ServiceState[];
}

export interface Overview {
  generated_at: string;
  hosts: OverviewHost[];
}

// Resolved identity of the calling token (GET /whoami). name is empty for the
// static bootstrap bearer.
export interface WhoAmI {
  name: string;
  role: string;
}

// ControlToken marshals with the ledger's JSON tags (snake_case).
export interface ControlToken {
  id: string;
  name: string;
  role: string;
  created_at: string;
  revoked_at?: string;
}

// Returned once by POST /tokens — the plaintext token is only ever shown here.
export interface CreatedToken {
  id: string;
  name: string;
  role: string;
  token: string;
}

// RepoWebhook has no server-side JSON tags, so it marshals with Go field names
// (PascalCase), like DeployRecord.
export interface RepoWebhook {
  ID: string;
  Service: string;
  CreatedAt: string;
}

// EnrollResult is the POST /enroll response: a single-use join token bound to a
// host. The SPKI cert pin can't be computed in-browser, so the full
// `shuttle agent join` one-liner is assembled by the operator's CLI.
export interface EnrollResult {
  id: string;
  host: string;
  join_token: string;
  expires_at_unix_ms: number;
}

export interface ShuttleEvent {
  type: string;
  service?: string;
  host?: string;
  deploy_id?: string;
  sha?: string;
  status?: string;
  message?: string;
  time: string;
  detail?: Record<string, string>;
}
