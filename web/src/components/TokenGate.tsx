import { useEffect, useState } from "react";
import { setToken } from "../auth";
import { verifyToken } from "../api";
import { getAuthConfig, startOidcLogin } from "../oidc";
import { Button } from "./ui";

export function TokenGate({
  onAuthed,
  initialError,
}: {
  onAuthed: () => void;
  initialError?: string | null;
}) {
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(initialError ?? null);
  const [busy, setBusy] = useState(false);
  const [oidcEnabled, setOidcEnabled] = useState(false);

  // Ask the orchestrator whether SSO is available so we can offer the button.
  useEffect(() => {
    getAuthConfig()
      .then((c) => setOidcEnabled(c.oidc_enabled))
      .catch(() => setOidcEnabled(false));
  }, []);

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

  async function sso() {
    setError(null);
    try {
      await startOidcLogin();
    } catch {
      setError("Could not start SSO login.");
    }
  }

  return (
    <div className="flex h-full items-center justify-center">
      <div className="w-80 border border-[var(--color-border)] bg-[var(--color-panel)] p-5">
        <div className="mb-1 text-sm font-medium">Shuttle</div>
        <div className="mb-4 text-xs text-[var(--color-muted)]">
          Sign in to the control plane.
        </div>

        {oidcEnabled && (
          <>
            <Button variant="primary" onClick={sso} className="w-full justify-center">
              Sign in with SSO
            </Button>
            <div className="my-3 flex items-center gap-2 text-[10px] uppercase tracking-wide text-[var(--color-muted)]">
              <span className="h-px flex-1 bg-[var(--color-border)]" />
              or token
              <span className="h-px flex-1 bg-[var(--color-border)]" />
            </div>
          </>
        )}

        <form onSubmit={submit}>
          <input
            autoFocus={!oidcEnabled}
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
    </div>
  );
}
