import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { ServiceState } from "../types";
import { Empty, Sha } from "../components/ui";

// Maps a reported container status to a health color.
function health(status: string): string {
  const s = status.toLowerCase();
  if (s.includes("run") || s.includes("healthy") || s === "up") return "var(--color-ok)";
  if (s.includes("exit") || s.includes("dead") || s.includes("unhealthy")) return "var(--color-err)";
  if (s.includes("restart") || s.includes("start") || s.includes("creat")) return "var(--color-warn)";
  return "var(--color-muted)";
}

function ago(iso: string): string {
  const s = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  return `${Math.floor(m / 60)}h`;
}

function ServiceRow({ s }: { s: ServiceState }) {
  return (
    <div className="flex items-center gap-3 border-t border-[var(--color-border)]/50 px-3 py-1.5 text-xs">
      <span className="inline-block h-2 w-2 shrink-0" style={{ background: health(s.status) }} />
      <span className="font-medium">{s.service}</span>
      <span className="mono text-[var(--color-muted)]">{s.status}</span>
      <span className="ml-auto flex items-center gap-3 text-[var(--color-muted)]">
        <Sha value={s.sha} />
        <span>{ago(s.reported_at)} ago</span>
      </span>
    </div>
  );
}

export function Overview() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["overview"],
    queryFn: () => api.overview(),
    refetchInterval: 5000,
  });

  if (isLoading) return <Empty>Loading…</Empty>;
  if (error) return <Empty>Failed to load overview.</Empty>;
  if (!data || data.hosts.length === 0)
    return <Empty>No hosts connected and no service state reported yet.</Empty>;

  return (
    <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
      {data.hosts.map((h) => {
        const unhealthy = h.services.filter((s) => health(s.status) === "var(--color-err)").length;
        return (
          <div key={h.name} className="border border-[var(--color-border)] bg-[var(--color-panel)]">
            <div className="flex items-center justify-between border-b border-[var(--color-border)] px-3 py-2">
              <div className="flex items-center gap-2">
                <span
                  className="inline-block h-2.5 w-2.5"
                  style={{ background: h.connected ? "var(--color-ok)" : "var(--color-err)" }}
                  title={h.connected ? "agent connected" : "agent offline"}
                />
                <span className="text-sm font-semibold">{h.name}</span>
                <span className="text-xs text-[var(--color-muted)]">
                  {h.connected ? "connected" : "offline"}
                  {h.last_seen && h.connected ? ` · seen ${ago(h.last_seen)} ago` : ""}
                </span>
              </div>
              <div className="text-xs text-[var(--color-muted)]">
                {h.services.length} svc
                {unhealthy > 0 && (
                  <span className="ml-2" style={{ color: "var(--color-err)" }}>
                    {unhealthy} down
                  </span>
                )}
              </div>
            </div>
            {h.services.length === 0 ? (
              <div className="px-3 py-4 text-center text-xs text-[var(--color-muted)]">
                No services reported.
              </div>
            ) : (
              h.services.map((s) => <ServiceRow key={s.service} s={s} />)
            )}
          </div>
        );
      })}
    </div>
  );
}
