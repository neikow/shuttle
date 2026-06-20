import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { DeployRecord } from "../types";
import { canDeploy } from "../role";
import { useRole } from "../role-context";
import { Button, Empty, Panel, Sha, StatusDot } from "../components/ui";
import { ConfirmDialog } from "../components/Modal";
import { useToast } from "../components/Toast";
import { LogsModal } from "./DeployLogs";

function ago(iso: string): string {
  const d = Date.now() - new Date(iso).getTime();
  const s = Math.floor(d / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

type Pending =
  | { kind: "redeploy"; service: string; sha: string }
  | { kind: "rollback"; service: string };

export function Deploys() {
  const role = useRole();
  const mayDeploy = canDeploy(role);
  const qc = useQueryClient();
  const toast = useToast();
  const [pending, setPending] = useState<Pending | null>(null);
  const [logsFor, setLogsFor] = useState<DeployRecord | null>(null);

  const { data, isLoading, error } = useQuery({
    queryKey: ["deploys"],
    queryFn: () => api.deploys(),
    refetchInterval: 5000,
  });

  const mut = useMutation({
    mutationFn: (p: Pending) =>
      p.kind === "redeploy" ? api.deploy(p.service, p.sha) : api.rollback(p.service, 1),
    onSuccess: (_res, p) => {
      toast.ok(`${p.kind === "redeploy" ? "Deploy" : "Rollback"} queued for ${p.service}.`);
      setPending(null);
      void qc.invalidateQueries({ queryKey: ["deploys"] });
      void qc.invalidateQueries({ queryKey: ["overview"] });
    },
    onError: (e) => toast.err(`Failed: ${(e as Error).message}`),
  });

  return (
    <Panel title="Deploy history">
      {isLoading ? (
        <Empty>Loading…</Empty>
      ) : error ? (
        <Empty>Failed to load deploys.</Empty>
      ) : !data || data.length === 0 ? (
        <Empty>No deploys recorded.</Empty>
      ) : (
        <table className="w-full text-left text-xs">
          <thead className="text-[var(--color-muted)]">
            <tr className="border-b border-[var(--color-border)]">
              <th className="px-3 py-2 font-medium">Service</th>
              <th className="px-3 py-2 font-medium">Host</th>
              <th className="px-3 py-2 font-medium">SHA</th>
              <th className="px-3 py-2 font-medium">Status</th>
              <th className="px-3 py-2 font-medium">Trigger</th>
              <th className="px-3 py-2 font-medium">When</th>
              <th className="px-3 py-2"></th>
            </tr>
          </thead>
          <tbody>
            {data.map((d: DeployRecord) => (
              <tr
                key={d.DeployID}
                className="border-b border-[var(--color-border)]/50 hover:bg-[var(--color-panel-2)]"
              >
                <td className="px-3 py-2 font-medium">{d.Service}</td>
                <td className="px-3 py-2 text-[var(--color-muted)]">{d.Host}</td>
                <td className="px-3 py-2">
                  <Sha value={d.SHA} />
                </td>
                <td className="px-3 py-2">
                  <StatusDot status={d.Status} />
                </td>
                <td className="px-3 py-2 text-[var(--color-muted)]">{d.TriggeredBy}</td>
                <td className="px-3 py-2 text-[var(--color-muted)]">{ago(d.StartedAt)}</td>
                <td className="px-3 py-2 text-right">
                  <span className="inline-flex gap-1.5">
                    <Button onClick={() => setLogsFor(d)}>Logs</Button>
                    {mayDeploy && (
                      <>
                        <Button
                          onClick={() =>
                            setPending({ kind: "redeploy", service: d.Service, sha: d.SHA })
                          }
                        >
                          Redeploy
                        </Button>
                        <Button onClick={() => setPending({ kind: "rollback", service: d.Service })}>
                          Rollback
                        </Button>
                      </>
                    )}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {pending && (
        <ConfirmDialog
          title={pending.kind === "redeploy" ? "Redeploy service" : "Rollback service"}
          message={
            pending.kind === "redeploy" ? (
              <>
                Redeploy <span className="font-medium">{pending.service}</span> at SHA{" "}
                <code className="mono">{pending.sha.slice(0, 12)}</code>?
              </>
            ) : (
              <>
                Roll <span className="font-medium">{pending.service}</span> back one deploy (to the
                previously-recorded SHA)?
              </>
            )
          }
          confirmLabel={pending.kind === "redeploy" ? "Redeploy" : "Rollback"}
          busy={mut.isPending}
          onConfirm={() => mut.mutate(pending)}
          onCancel={() => setPending(null)}
        />
      )}

      {logsFor && <LogsModal deploy={logsFor} onClose={() => setLogsFor(null)} />}
    </Panel>
  );
}
