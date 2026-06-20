import { useEffect, useState } from "react";
import * as Tabs from "@radix-ui/react-tabs";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  GitCompare,
  KeyRound,
  LayoutGrid,
  ListChecks,
  LogOut,
  Server,
  Webhook,
} from "lucide-react";
import { clearToken, getToken, setToken } from "./auth";
import { completeOidcLogin, oidcLogout } from "./oidc";
import { api, ApiError } from "./api";
import { canAdmin } from "./role";
import type { Role } from "./role";
import { RoleProvider } from "./role-context";
import { TokenGate } from "./components/TokenGate";
import { Empty } from "./components/ui";
import { Overview } from "./views/Overview";
import { Deploys } from "./views/Deploys";
import { Events } from "./views/Events";
import { Plan } from "./views/Plan";
import { Hosts } from "./views/Hosts";
import { Tokens } from "./views/Tokens";
import { Webhooks } from "./views/Webhooks";

export function App() {
  const [authed, setAuthed] = useState(() => !!getToken());
  // On first load, finish an OIDC redirect if this is the callback. Until that
  // resolves we hold rendering so the gate doesn't flash over a returning login.
  const [booting, setBooting] = useState(true);
  const [loginError, setLoginError] = useState<string | null>(null);

  useEffect(() => {
    completeOidcLogin()
      .then((idToken) => {
        if (idToken) {
          setToken(idToken);
          setAuthed(true);
        }
      })
      .catch(() => setLoginError("SSO login failed. Try again or use a token."))
      .finally(() => setBooting(false));
  }, []);

  function signOut() {
    clearToken();
    void oidcLogout();
    setAuthed(false);
  }

  if (booting) return <Empty>Loading…</Empty>;
  if (!authed) return <TokenGate onAuthed={() => setAuthed(true)} initialError={loginError} />;
  return <Shell onSignOut={signOut} />;
}

function Shell({ onSignOut }: { onSignOut: () => void }) {
  const who = useQuery({
    queryKey: ["whoami"],
    queryFn: () => api.whoami(),
    retry: false,
  });

  // A 401 means the stored token is no longer valid → drop back to the gate.
  // Done in an effect (not during render) so we don't update App while rendering.
  const unauthorized = who.error instanceof ApiError && who.error.status === 401;
  useEffect(() => {
    if (unauthorized) onSignOut();
  }, [unauthorized, onSignOut]);

  if (who.isLoading) {
    return <Empty>Authenticating…</Empty>;
  }
  if (who.error) {
    return unauthorized ? null : <Empty>Failed to resolve identity.</Empty>;
  }

  const role = (who.data?.role || "read") as Role;
  const name = who.data?.name;

  const tabs = [
    { id: "overview", label: "Overview", icon: LayoutGrid, el: <Overview /> },
    { id: "deploys", label: "Deploys", icon: ListChecks, el: <Deploys /> },
    { id: "events", label: "Events", icon: Activity, el: <Events /> },
    { id: "plan", label: "Plan", icon: GitCompare, el: <Plan /> },
    { id: "hosts", label: "Hosts", icon: Server, el: <Hosts /> },
  ];
  if (canAdmin(role)) {
    tabs.push({ id: "tokens", label: "Tokens", icon: KeyRound, el: <Tokens /> });
    tabs.push({ id: "webhooks", label: "Webhooks", icon: Webhook, el: <Webhooks /> });
  }

  return (
    <RoleProvider value={role}>
      <div className="mx-auto flex h-full max-w-6xl flex-col">
        <header className="flex items-center justify-between border-b border-[var(--color-border)] px-4 py-3">
          <div className="flex items-center gap-2">
            <span className="inline-block h-3 w-3 bg-[var(--color-accent)]" />
            <span className="text-sm font-semibold tracking-tight">Shuttle</span>
            <span className="text-xs text-[var(--color-muted)]">control plane</span>
            <span
              className="ml-1 border border-[var(--color-border)] bg-[var(--color-panel-2)] px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-[var(--color-muted)]"
              title={name ? `signed in as ${name}` : "static bearer"}
            >
              {role}
            </span>
          </div>
          <button
            onClick={onSignOut}
            className="inline-flex items-center gap-1.5 border border-[var(--color-border)] bg-[var(--color-panel-2)] px-2 py-1 text-xs hover:border-[var(--color-muted)]"
          >
            <LogOut size={12} /> Sign out
          </button>
        </header>

        <Tabs.Root defaultValue="overview" className="flex flex-1 flex-col overflow-hidden">
          <Tabs.List className="flex gap-1 border-b border-[var(--color-border)] px-3">
            {tabs.map((t) => (
              <Tabs.Trigger
                key={t.id}
                value={t.id}
                className="inline-flex items-center gap-1.5 border-b-2 border-transparent px-3 py-2 text-xs text-[var(--color-muted)] data-[state=active]:border-[var(--color-accent)] data-[state=active]:text-[var(--color-fg)]"
              >
                <t.icon size={13} />
                {t.label}
              </Tabs.Trigger>
            ))}
          </Tabs.List>
          <div className="flex-1 overflow-auto p-4">
            {tabs.map((t) => (
              <Tabs.Content key={t.id} value={t.id}>
                {t.el}
              </Tabs.Content>
            ))}
          </div>
        </Tabs.Root>
      </div>
    </RoleProvider>
  );
}
