import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import { Empty, Panel, Sha, StatusDot } from "../components/ui";

function ago(iso: string): string {
  const d = Date.now() - new Date(iso).getTime();
  const s = Math.floor(d / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

export function Deploys() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["deploys"],
    queryFn: () => api.deploys(),
    refetchInterval: 5000,
  });

  return (
    <Panel title="Deploy history">
      {isLoading ? (
        <Empty>Loading…</Empty>
      ) : error ? (
        <Empty>Failed to load deploys.</Empty>
      ) : !data || data.length === 0 ? (
        <Empty>No deploys recorded.</Empty>
      ) : (
        <table className="w-full text-left text-xs">
          <thead className="text-[var(--color-muted)]">
            <tr className="border-b border-[var(--color-border)]">
              <th className="px-3 py-2 font-medium">Service</th>
              <th className="px-3 py-2 font-medium">Host</th>
              <th className="px-3 py-2 font-medium">SHA</th>
              <th className="px-3 py-2 font-medium">Status</th>
              <th className="px-3 py-2 font-medium">Trigger</th>
              <th className="px-3 py-2 font-medium">When</th>
            </tr>
          </thead>
          <tbody>
            {data.map((d) => (
              <tr
                key={d.DeployID}
                className="border-b border-[var(--color-border)]/50 hover:bg-[var(--color-panel-2)]"
              >
                <td className="px-3 py-2 font-medium">{d.Service}</td>
                <td className="px-3 py-2 text-[var(--color-muted)]">{d.Host}</td>
                <td className="px-3 py-2">
                  <Sha value={d.SHA} />
                </td>
                <td className="px-3 py-2">
                  <StatusDot status={d.Status} />
                </td>
                <td className="px-3 py-2 text-[var(--color-muted)]">{d.TriggeredBy}</td>
                <td className="px-3 py-2 text-[var(--color-muted)]">{ago(d.StartedAt)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  );
}
