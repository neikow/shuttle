import { useState } from "react";
import { setToken } from "../auth";
import { verifyToken } from "../api";
import { Button } from "./ui";

export function TokenGate({ onAuthed }: { onAuthed: () => void }) {
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setToken(value.trim());
    const ok = await verifyToken();
    setBusy(false);
    if (ok) onAuthed();
    else setError("Token rejected by the orchestrator.");
  }

  return (
    <div className="flex h-full items-center justify-center">
      <form
        onSubmit={submit}
        className="w-80 border border-[var(--color-border)] bg-[var(--color-panel)] p-5"
      >
        <div className="mb-1 text-sm font-medium">Shuttle</div>
        <div className="mb-4 text-xs text-[var(--color-muted)]">
          Enter the control-plane bearer token.
        </div>
        <input
          autoFocus
          type="password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="bearer token"
          className="mono mb-3 w-full border border-[var(--color-border)] bg-[var(--color-bg)] px-2 py-1.5 text-xs outline-none focus:border-[var(--color-accent)]"
        />
        {error && <div className="mb-3 text-xs text-[var(--color-err)]">{error}</div>}
        <Button variant="primary" disabled={busy || !value.trim()}>
          {busy ? "Verifying…" : "Connect"}
        </Button>
      </form>
    </div>
  );
}
