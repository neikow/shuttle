import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { DeployRecord } from "../types";
import { Modal } from "../components/Modal";
import { Button } from "../components/ui";

// LogsModal shows the captured output of one deploy (GET /deploys/{id}/logs).
// Read-tier, so it's available to every signed-in role.
export function LogsModal({ deploy, onClose }: { deploy: DeployRecord; onClose: () => void }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ["deploy-logs", deploy.DeployID],
    queryFn: () => api.deployLogs(deploy.DeployID),
  });

  return (
    <Modal
      wide
      title={
        <span>
          Logs · <span className="text-[var(--color-fg)]">{deploy.Service}</span>{" "}
          <span className="mono text-[10px] normal-case">{deploy.SHA.slice(0, 12)}</span>
        </span>
      }
      onClose={onClose}
      footer={
        <Button onClick={onClose} variant="primary">
          Close
        </Button>
      }
    >
      {isLoading ? (
        <div className="text-[var(--color-muted)]">Loading…</div>
      ) : error ? (
        <div className="text-[var(--color-err)]">Failed to load logs.</div>
      ) : !data || data.length === 0 ? (
        <div className="text-[var(--color-muted)]">
          No output captured for this deploy. (Older deploys recorded before logs
          were stored, or a deploy that failed before the agent ran, have none.)
        </div>
      ) : (
        <pre className="mono max-h-[60vh] overflow-auto whitespace-pre-wrap break-words bg-[var(--color-bg)] p-2 text-[11px] leading-relaxed">
          {data.map((l, i) => (
            <div
              key={i}
              className={l.stream === "stderr" ? "text-[var(--color-err)]" : undefined}
            >
              {l.text}
            </div>
          ))}
        </pre>
      )}
    </Modal>
  );
}
