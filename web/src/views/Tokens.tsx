import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { ControlToken, CreatedToken } from "../types";
import { Button, Empty, Panel, Sha } from "../components/ui";
import { ConfirmDialog, Modal } from "../components/Modal";
import { useToast } from "../components/Toast";

const ROLES = ["read", "deploy", "admin"];

export function Tokens() {
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState("");
  const [role, setRole] = useState("read");
  const [created, setCreated] = useState<CreatedToken | null>(null);
  const [revoking, setRevoking] = useState<ControlToken | null>(null);

  const list = useQuery({ queryKey: ["tokens"], queryFn: () => api.listTokens() });

  const createMut = useMutation({
    mutationFn: () => api.createToken(name.trim(), role),
    onSuccess: (tok) => {
      setCreated(tok);
      setName("");
      void qc.invalidateQueries({ queryKey: ["tokens"] });
    },
    onError: (e) => toast.err(`Create failed: ${(e as Error).message}`),
  });

  const revokeMut = useMutation({
    mutationFn: (id: string) => api.revokeToken(id),
    onSuccess: () => {
      toast.ok("Token revoked.");
      setRevoking(null);
      void qc.invalidateQueries({ queryKey: ["tokens"] });
    },
    onError: (e) => toast.err(`Revoke failed: ${(e as Error).message}`),
  });

  return (
    <div className="space-y-4">
      <Panel
        title="Mint control token"
        actions={
          <div className="flex items-center gap-2">
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="name"
              aria-label="token name"
              className="mono w-40 border border-[var(--color-border)] bg-[var(--color-bg)] px-2 py-1 text-xs outline-none focus:border-[var(--color-accent)]"
            />
            <select
              value={role}
              onChange={(e) => setRole(e.target.value)}
              aria-label="token role"
              className="mono border border-[var(--color-border)] bg-[var(--color-bg)] px-2 py-1 text-xs outline-none focus:border-[var(--color-accent)]"
            >
              {ROLES.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
            <Button
              variant="primary"
              disabled={!name.trim() || createMut.isPending}
              onClick={() => createMut.mutate()}
            >
              {createMut.isPending ? "Minting…" : "Mint"}
            </Button>
          </div>
        }
      >
        <div className="px-3 py-2 text-xs text-[var(--color-muted)]">
          The plaintext token is shown once on creation — copy it then.
        </div>
      </Panel>

      <Panel title="Control tokens">
        {list.isLoading ? (
          <Empty>Loading…</Empty>
        ) : list.error ? (
          <Empty>Failed to load tokens.</Empty>
        ) : !list.data || list.data.length === 0 ? (
          <Empty>No control tokens.</Empty>
        ) : (
          <table className="w-full text-left text-xs">
            <thead className="text-[var(--color-muted)]">
              <tr className="border-b border-[var(--color-border)]">
                <th className="px-3 py-2 font-medium">Name</th>
                <th className="px-3 py-2 font-medium">Role</th>
                <th className="px-3 py-2 font-medium">ID</th>
                <th className="px-3 py-2 font-medium">Status</th>
                <th className="px-3 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {list.data.map((t) => (
                <tr key={t.id} className="border-b border-[var(--color-border)]/50">
                  <td className="px-3 py-2 font-medium">{t.name}</td>
                  <td className="px-3 py-2 text-[var(--color-muted)]">{t.role}</td>
                  <td className="px-3 py-2">
                    <Sha value={t.id} />
                  </td>
                  <td className="px-3 py-2 text-[var(--color-muted)]">
                    {t.revoked_at ? "revoked" : "active"}
                  </td>
                  <td className="px-3 py-2 text-right">
                    {!t.revoked_at && (
                      <Button onClick={() => setRevoking(t)}>Revoke</Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      {created && (
        <Modal
          title="Token created"
          onClose={() => setCreated(null)}
          footer={<Button variant="primary" onClick={() => setCreated(null)}>Done</Button>}
        >
          <div className="mb-2 text-[var(--color-muted)]">
            Copy this now — it is never shown again.
          </div>
          <code className="mono block break-all border border-[var(--color-border)] bg-[var(--color-bg)] p-2">
            {created.token}
          </code>
          <div className="mt-2 text-[var(--color-muted)]">
            {created.name} · {created.role}
          </div>
        </Modal>
      )}

      {revoking && (
        <ConfirmDialog
          title="Revoke token"
          message={
            <>
              Revoke <span className="font-medium">{revoking.name}</span> ({revoking.role})? It will
              stop authenticating immediately.
            </>
          }
          confirmLabel="Revoke"
          busy={revokeMut.isPending}
          onConfirm={() => revokeMut.mutate(revoking.id)}
          onCancel={() => setRevoking(null)}
        />
      )}
    </div>
  );
}
