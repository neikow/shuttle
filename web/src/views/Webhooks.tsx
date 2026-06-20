import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { RepoWebhook } from "../types";
import { Button, Empty, Panel, Sha } from "../components/ui";
import { ConfirmDialog, Modal } from "../components/Modal";
import { useToast } from "../components/Toast";

// triggerURL is the unauthenticated endpoint external systems POST to in order
// to redeploy the bound service; the 256-bit ID is the secret.
function triggerURL(id: string): string {
  return `${window.location.origin}/webhook/repo/${id}`;
}

export function Webhooks() {
  const qc = useQueryClient();
  const toast = useToast();
  const [service, setService] = useState("");
  const [createdURL, setCreatedURL] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<RepoWebhook | null>(null);

  const list = useQuery({ queryKey: ["webhooks"], queryFn: () => api.listWebhooks() });

  const createMut = useMutation({
    mutationFn: () => api.createWebhook(service.trim()),
    onSuccess: (res) => {
      setCreatedURL(triggerURL(res.id));
      setService("");
      void qc.invalidateQueries({ queryKey: ["webhooks"] });
    },
    onError: (e) => toast.err(`Create failed: ${(e as Error).message}`),
  });

  const deleteMut = useMutation({
    mutationFn: (id: string) => api.deleteWebhook(id),
    onSuccess: () => {
      toast.ok("Webhook deleted.");
      setDeleting(null);
      void qc.invalidateQueries({ queryKey: ["webhooks"] });
    },
    onError: (e) => toast.err(`Delete failed: ${(e as Error).message}`),
  });

  return (
    <div className="space-y-4">
      <Panel
        title="Create deploy webhook"
        actions={
          <div className="flex items-center gap-2">
            <input
              value={service}
              onChange={(e) => setService(e.target.value)}
              placeholder="service"
              aria-label="service name"
              className="mono w-40 border border-[var(--color-border)] bg-[var(--color-bg)] px-2 py-1 text-xs outline-none focus:border-[var(--color-accent)]"
            />
            <Button
              variant="primary"
              disabled={!service.trim() || createMut.isPending}
              onClick={() => createMut.mutate()}
            >
              {createMut.isPending ? "Creating…" : "Create"}
            </Button>
          </div>
        }
      >
        <div className="px-3 py-2 text-xs text-[var(--color-muted)]">
          A service-scoped webhook: POST to its URL triggers a redeploy. The URL contains the only
          secret — copy it on creation.
        </div>
      </Panel>

      <Panel title="Repo webhooks">
        {list.isLoading ? (
          <Empty>Loading…</Empty>
        ) : list.error ? (
          <Empty>Failed to load webhooks.</Empty>
        ) : !list.data || list.data.length === 0 ? (
          <Empty>No repo webhooks.</Empty>
        ) : (
          <table className="w-full text-left text-xs">
            <thead className="text-[var(--color-muted)]">
              <tr className="border-b border-[var(--color-border)]">
                <th className="px-3 py-2 font-medium">Service</th>
                <th className="px-3 py-2 font-medium">ID</th>
                <th className="px-3 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {list.data.map((w) => (
                <tr key={w.ID} className="border-b border-[var(--color-border)]/50">
                  <td className="px-3 py-2 font-medium">{w.Service}</td>
                  <td className="px-3 py-2">
                    <Sha value={w.ID} />
                  </td>
                  <td className="px-3 py-2 text-right">
                    <Button onClick={() => setDeleting(w)}>Delete</Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      {createdURL && (
        <Modal
          title="Webhook created"
          onClose={() => setCreatedURL(null)}
          footer={<Button variant="primary" onClick={() => setCreatedURL(null)}>Done</Button>}
        >
          <div className="mb-2 text-[var(--color-muted)]">
            POST to this URL to redeploy the service. Treat it as a secret.
          </div>
          <code className="mono block break-all border border-[var(--color-border)] bg-[var(--color-bg)] p-2">
            {createdURL}
          </code>
        </Modal>
      )}

      {deleting && (
        <ConfirmDialog
          title="Delete webhook"
          message={
            <>
              Delete the webhook for <span className="font-medium">{deleting.Service}</span>? Any
              system using its URL will stop working.
            </>
          }
          confirmLabel="Delete"
          busy={deleteMut.isPending}
          onConfirm={() => deleteMut.mutate(deleting.ID)}
          onCancel={() => setDeleting(null)}
        />
      )}
    </div>
  );
}
