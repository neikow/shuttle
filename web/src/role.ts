// Mirrors the orchestrator's RBAC tiers (internal/orchestrator/rbac.go): the
// roles are totally ordered read < deploy < admin. The UI uses these helpers to
// decide which mutation screens and actions to show. This is convenience only —
// the server enforces the real gate via requireRole; a forged role here just
// earns a 401/403 on the actual request.
export type Role = "read" | "deploy" | "admin";

export function roleRank(role: Role | string): number {
  switch (role) {
    case "read":
      return 1;
    case "deploy":
      return 2;
    case "admin":
      return 3;
    default:
      return 0;
  }
}

// canDeploy reports whether the role may run operational mutations
// (deploy/rollback/prune).
export function canDeploy(role: Role | string): boolean {
  return roleRank(role) >= roleRank("deploy");
}

// canAdmin reports whether the role may run admin mutations
// (token/webhook/enrollment management).
export function canAdmin(role: Role | string): boolean {
  return roleRank(role) >= roleRank("admin");
}
