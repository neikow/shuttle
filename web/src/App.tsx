import { useState } from "react";
import * as Tabs from "@radix-ui/react-tabs";
import { Activity, GitCompare, LayoutGrid, ListChecks, Server, LogOut } from "lucide-react";
import { clearToken, getToken } from "./auth";
import { TokenGate } from "./components/TokenGate";
import { Overview } from "./views/Overview";
import { Deploys } from "./views/Deploys";
import { Events } from "./views/Events";
import { Plan } from "./views/Plan";
import { Hosts } from "./views/Hosts";

const TABS = [
  { id: "overview", label: "Overview", icon: LayoutGrid, el: <Overview /> },
  { id: "deploys", label: "Deploys", icon: ListChecks, el: <Deploys /> },
  { id: "events", label: "Events", icon: Activity, el: <Events /> },
  { id: "plan", label: "Plan", icon: GitCompare, el: <Plan /> },
  { id: "hosts", label: "Hosts", icon: Server, el: <Hosts /> },
];

export function App() {
  const [authed, setAuthed] = useState(() => !!getToken());

  if (!authed) return <TokenGate onAuthed={() => setAuthed(true)} />;

  return (
    <div className="mx-auto flex h-full max-w-6xl flex-col">
      <header className="flex items-center justify-between border-b border-[var(--color-border)] px-4 py-3">
        <div className="flex items-center gap-2">
          <span className="inline-block h-3 w-3 bg-[var(--color-accent)]" />
          <span className="text-sm font-semibold tracking-tight">Shuttle</span>
          <span className="text-xs text-[var(--color-muted)]">control plane</span>
        </div>
        <button
          onClick={() => {
            clearToken();
            setAuthed(false);
          }}
          className="inline-flex items-center gap-1.5 border border-[var(--color-border)] bg-[var(--color-panel-2)] px-2 py-1 text-xs hover:border-[var(--color-muted)]"
        >
          <LogOut size={12} /> Sign out
        </button>
      </header>

      <Tabs.Root defaultValue="overview" className="flex flex-1 flex-col overflow-hidden">
        <Tabs.List className="flex gap-1 border-b border-[var(--color-border)] px-3">
          {TABS.map((t) => (
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
          {TABS.map((t) => (
            <Tabs.Content key={t.id} value={t.id}>
              {t.el}
            </Tabs.Content>
          ))}
        </div>
      </Tabs.Root>
    </div>
  );
}
