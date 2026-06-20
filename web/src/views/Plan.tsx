import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { PlanAction } from "../types";
import { canDeploy } from "../role";
import { useRole } from "../role-context";
import { Button, Empty, Panel, Sha } from "../components/ui";
import { ConfirmDialog } from "../components/Modal";
import { useToast } from "../components/Toast";

const ACTION: Record<PlanAction, { sym: string; color: string }> = {
  create: { sym: "+", color: "var(--color-ok)" },
  update: { sym: "~", color: "var(--color-warn)" },
  remove: { sym: "-", color: "var(--color-err)" },
  unchanged: { sym: " ", color: "var(--color-muted)" },
};

export function Plan() {
  const role = useRole();
  const mayDeploy = canDeploy(role);
  const qc = useQueryClient();
  const toast = useToast();
  const [pending, setPending] = useState<{ service: string; sha: string } | null>(null);
  const [ref, setRef] = useState("");
  const [submittedRef, setSubmittedRef] = useState<string | undefined>(undefined);
  const [tick, setTick] = useState(0);

  const deployMut = useMutation({
    mutationFn: (p: { service: string; sha: string }) => api.deploy(p.service, p.sha),
    onSuccess: (_res, p) => {
      toast.ok(`Deploy queued for ${p.service}.`);
      setPending(null);
      void qc.invalidateQueries({ queryKey: ["deploys"] });
      void qc.invalidateQueries({ queryKey: ["overview"] });
    },
    onError: (e) => toast.err(`Deploy failed: ${(e as Error).message}`),
  });

  const plan = useQuery({
    queryKey: ["plan", submittedRef, tick],
    queryFn: () => api.plan(submittedRef),
  });
  const check = useQuery({
    queryKey: ["check", submittedRef, tick],
    queryFn: () => api.check(submittedRef),
  });

  function run() {
    setSubmittedRef(ref.trim() || undefined);
    setTick((t) => t + 1);
  }

  const counts = plan.data
    ? plan.data.services.reduce<Record<string, number>>((acc, s) => {
        acc[s.action] = (acc[s.action] ?? 0) + 1;
        return acc;
      }, {})
    : {};

  return (
    <div className="space-y-4">
      <Panel
        title="Plan / check"
        actions={
          <div className="flex items-center gap-2">
            <input
              value={ref}
              onChange={(e) => setRef(e.target.value)}
              placeholder="git ref (optional)"
              className="mono w-48 border border-[var(--color-border)] bg-[var(--color-bg)] px-2 py-1 text-xs outline-none focus:border-[var(--color-accent)]"
            />
            <Button variant="primary" onClick={run} disabled={plan.isFetching || check.isFetching}>
              {plan.isFetching || check.isFetching ? "Running…" : "Run"}
            </Button>
          </div>
        }
      >
        {plan.error ? (
          <Empty>Plan failed: {String((plan.error as Error).message)}</Empty>
        ) : !plan.data ? (
          <Empty>Run a plan to preview the desired-vs-deployed diff.</Empty>
        ) : (
          <div>
            <div className="flex items-center gap-4 border-b border-[var(--color-border)] px-3 py-2 text-xs text-[var(--color-muted)]">
              <span>
                against <Sha value={plan.data.sha} />
              </span>
              <span style={{ color: ACTION.create.color }}>{counts.create ?? 0} create</span>
              <span style={{ color: ACTION.update.color }}>{counts.update ?? 0} update</span>
              <span style={{ color: ACTION.remove.color }}>{counts.remove ?? 0} remove</span>
              <span>{counts.unchanged ?? 0} unchanged</span>
            </div>
            {plan.data.services.map((s) => (
              <div
                key={s.service}
                className="flex items-center gap-3 border-b border-[var(--color-border)]/50 px-3 py-1.5 text-xs"
              >
                <span
                  className="mono w-4 text-center font-bold"
                  style={{ color: ACTION[s.action].color }}
                >
                  {ACTION[s.action].sym}
                </span>
                <span className="w-20" style={{ color: ACTION[s.action].color }}>
                  {s.action}
                </span>
                <span className="font-medium">{s.service}</span>
                {s.host && <span className="text-[var(--color-muted)]">@{s.host}</span>}
                <span className="ml-auto flex items-center gap-3">
                  {s.action === "update" && (
                    <span className="text-[var(--color-muted)]">
                      <Sha value={s.current_sha} /> → <Sha value={s.desired_sha} />
                    </span>
                  )}
                  {mayDeploy && (s.action === "create" || s.action === "update") && s.desired_sha && (
                    <Button
                      onClick={() => setPending({ service: s.service, sha: s.desired_sha! })}
                    >
                      Deploy
                    </Button>
                  )}
                </span>
              </div>
            ))}
          </div>
        )}
      </Panel>

      <Panel title="Validation (check)">
        {check.error ? (
          <Empty>Check failed: {String((check.error as Error).message)}</Empty>
        ) : !check.data ? (
          <Empty>—</Empty>
        ) : !check.data.services || check.data.services.length === 0 ? (
          <Empty>No services.</Empty>
        ) : (
          <div>
            {check.data.services.map((s) => {
              const ok = !s.error && (s.missing_keys?.length ?? 0) === 0;
              return (
                <div
                  key={s.service}
                  className="flex items-start gap-3 border-b border-[var(--color-border)]/50 px-3 py-1.5 text-xs"
                >
                  <span style={{ color: ok ? "var(--color-ok)" : "var(--color-err)" }}>
                    {ok ? "✓" : "✗"}
                  </span>
                  <span className="font-medium">{s.service}</span>
                  <span className="text-[var(--color-muted)]">
                    {s.error
                      ? s.error
                      : s.missing_keys?.length
                        ? `missing: ${s.missing_keys.join(", ")}`
                        : "ok"}
                    {s.warnings?.map((w) => (
                      <span key={w} className="ml-2" style={{ color: "var(--color-warn)" }}>
                        ! {w}
                      </span>
                    ))}
                  </span>
                </div>
              );
            })}
          </div>
        )}
      </Panel>

      {pending && (
        <ConfirmDialog
          title="Deploy service"
          message={
            <>
              Deploy <span className="font-medium">{pending.service}</span> at SHA{" "}
              <code className="mono">{pending.sha.slice(0, 12)}</code>?
            </>
          }
          confirmLabel="Deploy"
          busy={deployMut.isPending}
          onConfirm={() => deployMut.mutate(pending)}
          onCancel={() => setPending(null)}
        />
      )}
    </div>
  );
}
