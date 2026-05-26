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
