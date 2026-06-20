import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import type { EnrollResult } from "../types";
import { canAdmin } from "../role";
import { useRole } from "../role-context";
import { Button, Empty, Panel } from "../components/ui";
import { Modal } from "../components/Modal";
import { useToast } from "../components/Toast";

export function Hosts() {
  const role = useRole();
  const mayEnroll = canAdmin(role);
  const toast = useToast();
  const [enrolled, setEnrolled] = useState<EnrollResult | null>(null);

  const { data, isLoading, error } = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api.hosts(),
    refetchInterval: 15000,
  });

  const enrollMut = useMutation({
    mutationFn: (host: string) => api.enroll(host),
    onSuccess: (res) => setEnrolled(res),
    onError: (e) => toast.err(`Enroll failed: ${(e as Error).message}`),
  });

  return (
    <Panel title="Declared hosts">
      {isLoading ? (
        <Empty>Loading…</Empty>
      ) : error ? (
        <Empty>
          {error instanceof ApiError && error.status === 404
            ? "Host listing is not enabled on this orchestrator (enrollment off)."
            : "Failed to load hosts."}
        </Empty>
      ) : !data || data.length === 0 ? (
        <Empty>No hosts declared.</Empty>
      ) : (
        <table className="w-full text-left text-xs">
          <thead className="text-[var(--color-muted)]">
            <tr className="border-b border-[var(--color-border)]">
              <th className="px-3 py-2 font-medium">Host</th>
              <th className="px-3 py-2 font-medium">Labels</th>
              {mayEnroll && <th className="px-3 py-2"></th>}
            </tr>
          </thead>
          <tbody>
            {data.map((h) => (
              <tr key={h.name} className="border-b border-[var(--color-border)]/50">
                <td className="px-3 py-2 font-medium">{h.name}</td>
                <td className="px-3 py-2 text-[var(--color-muted)]">
                  {h.labels && Object.keys(h.labels).length > 0
                    ? Object.entries(h.labels)
                        .map(([k, v]) => `${k}=${v}`)
                        .join("  ")
                    : "—"}
                </td>
                {mayEnroll && (
                  <td className="px-3 py-2 text-right">
                    <Button
                      disabled={enrollMut.isPending}
                      onClick={() => enrollMut.mutate(h.name)}
                    >
                      Enroll
                    </Button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {enrolled && (
        <Modal
          title={`Join token for ${enrolled.host}`}
          onClose={() => setEnrolled(null)}
          footer={<Button variant="primary" onClick={() => setEnrolled(null)}>Done</Button>}
        >
          <div className="mb-2 text-[var(--color-muted)]">
            Single-use, expires {new Date(enrolled.expires_at_unix_ms).toLocaleString()}. Copy it
            now.
          </div>
          <code className="mono block break-all border border-[var(--color-border)] bg-[var(--color-bg)] p-2">
            {enrolled.join_token}
          </code>
          <div className="mt-2 text-[var(--color-muted)]">
            Redeem on the host with <code className="mono">shuttle agent join</code>. The cert pin
            can't be computed in-browser — run <code className="mono">shuttle enroll</code> from a
            trusted machine for the fully-pinned one-liner.
          </div>
        </Modal>
      )}
    </Panel>
  );
}
